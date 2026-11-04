package codebuddy

// CodeBuddy 额度（积分类）读取。
//
// 数据源与 aether / local-vault 一致，都是腾讯的 meter 计费接口：
//   - POST {base}/v2/billing/meter/get-user-resource      资源包列表（主路径）
//   - POST {base}/v2/billing/meter/get-enterprise-user-usage  企业成员账号的用量
//
// 三个关键差异（都是"看起来能用、数字却不对"的根源）：
//  1. CodeBuddy 的 get-user-resource 请求体用「包结束时间区间」而不是 WorkBuddy 的
//     OnlyValidPeriod=true。两者都能列出资源包，但上游对 CodeBuddy 只认区间形态
//     （参考实现的测试明确断言 OnlyValidPeriod 必须缺省）。
//  2. 企业成员账号打 get-user-resource 会返回「空 Accounts」而不是报错，
//     额度在企业用量接口里；不回落就会出现"企业账号永远没有额度"。
//  3. 一个账号常常同时有主套餐、赠送包、活动包、加量包。它们容量各自独立，
//     只有同一 PackageCode 的分页条目才能相加；加量包（TCACA_code_009）
//     必须单列，混进主额度会让"还剩多少"凭空变大。

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"aether-cockpit/internal/core/ir"
	"aether-cockpit/internal/provider"
)

const (
	cbMeterResourcePath   = "/v2/billing/meter/get-user-resource"
	cbMeterEnterprisePath = "/v2/billing/meter/get-enterprise-user-usage"
	// cbMeterProductCode 是 CodeBuddy 的资源包产品号（与 WorkBuddy 相同）。
	cbMeterProductCode = "p_tcaca"
	// cbExtraPackageCode 是「加量包」：与主套餐是两个独立额度池，只单列、不相加。
	cbExtraPackageCode = "TCACA_code_009_0XmEQc2xOf"
	// cbPackageTimeLayout 是请求体里包结束时间的格式，上游按本地时间解析。
	cbPackageTimeLayout = "2006-01-02 15:04:05"
)

// QueryQuota 读取 CodeBuddy 的剩余积分（credits）。
//
// 积分类账号没有"限流窗口"概念：这里给出一张 Credits 窗口（百分比 = 已用/总量）
// 加一条余额行，加量包作为 Aside 窗口单列。
func (p *Provider) QueryQuota(ctx context.Context, env *provider.AccountEnv) (*provider.QuotaStatus, error) {
	token := strings.TrimSpace(cbFirst(env.GetCredential("token"), env.GetCredential("access_token")))
	if token == "" {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"CodeBuddy 账号缺少 access_token；请重新登录或手动粘贴 Token").WithProvider(p.profile.id)
	}
	base := strings.TrimRight(strings.TrimSpace(env.GetCredential("base_url")), "/")
	if base == "" {
		base = p.profile.baseURL
	}

	accounts, shape, err := p.cbFetchAccounts(ctx, env, base, token)
	if err != nil {
		return nil, err
	}

	// 企业成员账号：get-user-resource 对它们返回空 Accounts（是"这段账号没有个人资源包"，
	// 不是错误）。所以空列表必须回落到企业用量接口，否则这类账号永远读不到额度。
	if len(accounts) == 0 {
		reason := "响应里没有有效资源包（Accounts 为空）"
		if !shape {
			reason = "响应里找不到 Accounts 字段（可能是 base_url 或客户端版本不对）"
		}
		ent, entErr := p.cbFetchEnterprise(ctx, env, base, token)
		if entErr == nil {
			return ent, nil
		}
		if strings.TrimSpace(env.GetCredential("enterprise_id")) == "" {
			// 没有 Enterprise ID 时企业接口不可能成功，直接给出可执行的建议，
			// 而不是把上游的 403 原样抛给用户。
			return nil, ir.NewAPIErrorf(ir.ErrCodeNoProvider,
				"CodeBuddy 读取不到额度：%s；企业成员账号请补填 Enterprise ID（额度在企业用量接口里）。企业接口返回：%s",
				reason, entErr.Error()).WithProvider(p.profile.id)
		}
		return nil, entErr
	}

	pkgs := cbAggregatePackages(accounts)
	main, extra := cbSplitPackages(pkgs)

	// 只有加量包、没有任何主套餐时，仍然给出读数（把加量包当主额度），
	// 否则界面会显示 0，比"显示加量包"更误导。
	if cbSumTotal(main) <= 0 && len(extra) > 0 {
		main, extra = extra, nil
	}

	q := &provider.QuotaStatus{
		ReadAt:      time.Now().Unix(),
		Unit:        "credits",
		BalanceUnit: "credits",
	}

	total, remain := 0.0, 0.0
	reset := int64(0)
	for _, pkg := range main {
		total += pkg.Total
		remain += pkg.Remain
		reset = cbEarliest(reset, pkg.Reset)
	}
	// used 一律由 total - remain 反算：上游另有一个 Used 字段，但它与 Remain
	// 在跨周期/赠送场景下并不总是自洽，直接信它会让进度条和余额互相矛盾。
	used := cbClamp(total-remain, 0, total)
	q.Total, q.Used, q.Remaining = int(cbRound(total)), int(cbRound(used)), int(cbRound(remain))
	q.Balance = remain
	if len(main) > 0 {
		q.Windows = append(q.Windows, provider.QuotaWindow{
			Name:     "Credits",
			Used:     cbPercent(used, total),
			Amount:   cbRound(used),
			Limit:    cbRound(total),
			Unit:     "credits",
			ResetsAt: reset,
		})
	}
	for _, pkg := range extra {
		usedExtra := cbClamp(pkg.Total-pkg.Remain, 0, pkg.Total)
		q.Windows = append(q.Windows, provider.QuotaWindow{
			Name:     "加量包",
			Used:     cbPercent(usedExtra, pkg.Total),
			Amount:   cbRound(usedExtra),
			Limit:    cbRound(pkg.Total),
			Unit:     "credits",
			Aside:    true,
			ResetsAt: pkg.Reset,
		})
	}
	if len(pkgs) == 0 {
		// Accounts 里确实有条目，却一个容量字段都没解析出来：这不是失败，
		// 而是一次"读到了但读不懂"的读数，留下提示让用户/我们能看到。
		q.Message = "CodeBuddy 资源包里没有容量字段（CycleCapacity* / Capacity* 均缺失）"
	}
	return q, nil
}

/* ---------------- 请求 ---------------- */

// cbFetchAccounts 拉取个人资源包列表。
// 第二个返回值 shape 表示「响应里到底有没有 Accounts 数组」：
// shape=false → 路径/版本不对；shape=true 且列表为空 → 该账号没有个人资源包。
func (p *Provider) cbFetchAccounts(ctx context.Context, env *provider.AccountEnv, base, token string) ([]cbResource, bool, error) {
	now := time.Now()
	payload := map[string]any{
		"PageNumber":  1,
		"PageSize":    100,
		"ProductCode": cbMeterProductCode,
		"Status":      []int{0, 3}, // 0=有效，3=有效（待生效）
		// 用时间区间而不是 OnlyValidPeriod：CodeBuddy 只认这一种形态。
		// 区间取"现在 ~ 101 年后"，等于"所有未过期的包"，与参考实现一致。
		"PackageEndTimeRangeBegin": now.Format(cbPackageTimeLayout),
		"PackageEndTimeRangeEnd":   now.AddDate(101, 0, 0).Format(cbPackageTimeLayout),
	}
	body, err := p.cbMeterCall(ctx, env, base, token, cbMeterResourcePath, payload)
	if err != nil {
		return nil, false, err
	}
	items, shape := cbAccountsFromBody(body)
	return items, shape, nil
}

// cbFetchEnterprise 读取企业成员账号的周期用量。
func (p *Provider) cbFetchEnterprise(ctx context.Context, env *provider.AccountEnv, base, token string) (*provider.QuotaStatus, error) {
	body, err := p.cbMeterCall(ctx, env, base, token, cbMeterEnterprisePath, map[string]any{})
	if err != nil {
		return nil, err
	}
	usage, ok := cbEnterpriseFromBody(body)
	if !ok {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"CodeBuddy 企业用量响应无法解析").WithProvider(p.profile.id)
	}

	limit := cbFirstNum(usage.LimitNum, usage.LimitNumSnake)
	if !limit.OK || limit.Value <= 0 {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed,
			"CodeBuddy 企业用量缺少 limitNum/limit_num").WithProvider(p.profile.id)
	}
	// 已用优先 used_num，其次旧的 credit 字段（两者语义相同：本周期已消耗）。
	used := cbFirstNum(usage.UsedNum, usage.UsedNumSnake, usage.Credit)
	usedValue := cbClamp(used.Value, 0, limit.Value)
	if !used.OK {
		usedValue = 0
	}
	remain := limit.Value - usedValue

	q := &provider.QuotaStatus{
		ReadAt:      time.Now().Unix(),
		Total:       int(cbRound(limit.Value)),
		Used:        int(cbRound(usedValue)),
		Remaining:   int(cbRound(remain)),
		Unit:        "credits",
		Balance:     remain,
		BalanceUnit: "credits",
		Windows: []provider.QuotaWindow{{
			Name:     "Credits",
			Used:     cbPercent(usedValue, limit.Value),
			Amount:   cbRound(usedValue),
			Limit:    cbRound(limit.Value),
			Unit:     "credits",
			ResetsAt: cbEnterpriseReset(usage),
		}},
	}
	return q, nil
}

// cbEnterpriseReset 取企业用量的周期结束时间；字段缺失时退回重置时间。
func cbEnterpriseReset(u *cbEnterpriseUsage) int64 {
	if ts := cbFirstTime(u.CycleEndTime, u.CycleEndSnake).Unix(); ts > 0 {
		return ts
	}
	return cbFirstTime(u.CycleResetTime, u.CycleResetSnake).Unix()
}

// cbMeterCall 发一次 meter 请求并做错误分类。
//
// 401/403/404 分别给不同的文案：401 说明登录态坏了（用户要重新登录），
// 403 多半是企业账号缺 Enterprise ID，404 说明 base_url/版本压根没有这个接口
// ——三者对用户动作的要求完全不同，压成"读取失败"就没法排查。
func (p *Provider) cbMeterCall(ctx context.Context, env *provider.AccountEnv, base, token, path string, payload any) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "CodeBuddy 额度请求序列化失败").WithCause(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(raw))
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeInternal, "构造 CodeBuddy 额度请求失败").WithCause(err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("User-Agent", "CLI/"+cbCLIVersion+" CodeBuddy/"+cbCLIVersion)

	// 账号级头：能拿到就带，缺就省略（上游把空串当非法租户值，不能设空头）。
	if uid := cbQuotaUID(env, token); uid != "" {
		req.Header.Set("X-User-Id", uid)
	}
	if ent := strings.TrimSpace(env.GetCredential("enterprise_id")); ent != "" {
		req.Header.Set("X-Enterprise-Id", ent)
		req.Header.Set("X-Tenant-Id", cbFirst(strings.TrimSpace(env.GetCredential("tenant_id")), ent))
	}
	if domain := strings.TrimSpace(env.GetCredential("domain")); domain != "" {
		req.Header.Set("X-Domain", domain)
	} else if u, err := url.Parse(base); err == nil && u.Host != "" {
		req.Header.Set("X-Domain", u.Host)
	}

	resp, err := env.Client().Do(req)
	if err != nil {
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamFailed, "CodeBuddy 额度接口连接失败").
			WithCause(err).WithProvider(p.profile.id).WithRetryable(true)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		// 只报错、不标账号失效：额度是展示用途，一次读数失败不该把能聊天的账号下线。
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"CodeBuddy 登录态已失效（额度接口 401）；请重新登录或重新粘贴 Token").WithProvider(p.profile.id)
	case http.StatusForbidden:
		return nil, ir.NewAPIError(ir.ErrCodeUpstreamAuth,
			"CodeBuddy 额度接口拒绝访问（403）；企业成员账号需要填 Enterprise ID").WithProvider(p.profile.id)
	case http.StatusNotFound:
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed,
			"CodeBuddy 该域名没有额度接口（404：%s）；请确认 base_url 指向官方站点（codebuddy.ai / codebuddy.cn）",
			path).WithProvider(p.profile.id)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "CodeBuddy 额度接口返回 %d：%s",
			resp.StatusCode, truncateText(strings.TrimSpace(string(body)), 300)).WithProvider(p.profile.id)
	}
	if code := cbBodyCode(body); code != 0 && code != 200 {
		return nil, ir.NewAPIErrorf(ir.ErrCodeUpstreamFailed, "CodeBuddy 额度接口返回业务码 %d：%s",
			code, truncateText(strings.TrimSpace(string(body)), 300)).WithProvider(p.profile.id)
	}
	return body, nil
}

// cbQuotaUID 取账号 id：优先账号级 uid，其次 JWT 的 sub（国际站 JWT 里带）。
func cbQuotaUID(env *provider.AccountEnv, token string) string {
	for _, key := range []string{"account_uid", "uid", "user_id"} {
		if v := strings.TrimSpace(env.GetCredential(key)); v != "" {
			return v
		}
	}
	return jwtSubject(token)
}

// cbBodyCode 取业务码：有些端点把它嵌在 error 对象里。
func cbBodyCode(body []byte) int {
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return 0
	}
	if nested, ok := root["error"].(map[string]any); ok {
		root = nested
	}
	v, ok := root["code"]
	if !ok {
		return 0
	}
	return int(anyInt(v))
}

/* ---------------- 响应解析 ---------------- */

// cbResource 是 Accounts[] 里的一条资源包。
//
// 键名用自定义 UnmarshalJSON 读：上游 PascalCase 为主，但 CN 站点偶有
// snake_case 变体，用大小写/下划线双拼才不用为每个变体写一个结构体。
type cbResource struct {
	PackageCode  string
	PackageName  string
	Status       cbNum
	CycleEndTime cbTime
	ExpiredTime  cbTime

	CycleCapacitySize   cbNum
	CycleCapacityUsed   cbNum
	CycleCapacityRemain cbNum
	CapacitySize        cbNum
	CapacityUsed        cbNum
	CapacityRemain      cbNum
}

// UnmarshalJSON 逐字段取，缺失即"不存在"（不是 0）。
func (r *cbResource) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	r.PackageCode = cbStringField(raw, "PackageCode", "package_code")
	r.PackageName = cbStringField(raw, "PackageName", "package_name")
	r.Status = cbNumFrom(cbRawField(raw, "Status", "status"))
	r.CycleEndTime = cbTimeFrom(cbRawField(raw, "CycleEndTime", "cycle_end_time"))
	r.ExpiredTime = cbTimeFrom(cbRawField(raw, "ExpiredTime", "expired_time"))
	// Capacity 家族一律 *Precise 优先：它是未取整的原始值，
	// 用取整值会让"还剩 0.6"显示成"还剩 0"。
	r.CycleCapacitySize = cbNumFrom(cbRawField(raw,
		"CycleCapacitySizePrecise", "cycle_capacity_size_precise", "CycleCapacitySize", "cycle_capacity_size"))
	r.CycleCapacityUsed = cbNumFrom(cbRawField(raw,
		"CycleCapacityUsedPrecise", "cycle_capacity_used_precise", "CycleCapacityUsed", "cycle_capacity_used"))
	r.CycleCapacityRemain = cbNumFrom(cbRawField(raw,
		"CycleCapacityRemainPrecise", "cycle_capacity_remain_precise", "CycleCapacityRemain", "cycle_capacity_remain"))
	r.CapacitySize = cbNumFrom(cbRawField(raw,
		"CapacitySizePrecise", "capacity_size_precise", "CapacitySize", "capacity_size"))
	r.CapacityUsed = cbNumFrom(cbRawField(raw,
		"CapacityUsedPrecise", "capacity_used_precise", "CapacityUsed", "capacity_used"))
	r.CapacityRemain = cbNumFrom(cbRawField(raw,
		"CapacityRemainPrecise", "capacity_remain_precise", "CapacityRemain", "capacity_remain"))
	return nil
}

// cbTotals 取「本周期总量 / 剩余」。
//
// Cycle 优先于 Capacity 是硬规则：Capacity 是账号级总池，CycleCapacity 才是
// 本计费周期；反过来取会把换月后的历史总量当成"当前额度"。
// Used 字段不参与计算——统一由 total - remain 反算，保证三个数自洽。
func (r cbResource) cbTotals() (total, remain float64, ok bool) {
	candidates := [][3]cbNum{
		{r.CycleCapacitySize, r.CycleCapacityRemain, r.CycleCapacityUsed},
		{r.CapacitySize, r.CapacityRemain, r.CapacityUsed},
	}
	for _, pair := range candidates {
		if !pair[0].OK {
			continue
		}
		total = math.Max(pair[0].Value, 0)
		switch {
		case pair[1].OK:
			// 有剩余字段就直接用。
			remain = pair[1].Value
		case pair[2].OK:
			// 只有"已用"时反算剩余。不能默认"未使用"：有些版本只回
			// Used，按 total 当剩余会让用光的账号显示成满额。
			remain = total - pair[2].Value
		default:
			remain = total
		}
		remain = cbClamp(remain, 0, total)
		return total, remain, true
	}
	return 0, 0, false
}

// cbReset 是本包的重置时间：周期结束优先，其次额度到期。
func (r cbResource) cbReset() int64 {
	if ts := r.CycleEndTime.Unix(); ts > 0 {
		return ts
	}
	return r.ExpiredTime.Unix()
}

// cbAccountsFromBody 从 user-resource 响应里取出 Accounts 数组。
//
// 参考实现会逐路径尝试 5 种包裹形态；我们照抄这个容错，
// 因为不同站点/版本的包裹层确实不一样，只认一种就会把"读到了"当成"没数据"。
func cbAccountsFromBody(body []byte) ([]cbResource, bool) {
	var root any
	if json.Unmarshal(body, &root) != nil {
		return nil, false
	}
	for _, path := range cbAccountPaths {
		v, ok := cbWalk(root, path)
		if !ok {
			continue
		}
		arr, ok := v.([]any)
		if !ok {
			continue
		}
		out := make([]cbResource, 0, len(arr))
		for _, item := range arr {
			raw, err := json.Marshal(item)
			if err != nil {
				continue
			}
			var r cbResource
			if json.Unmarshal(raw, &r) != nil {
				continue
			}
			out = append(out, r)
		}
		return out, true
	}
	return nil, false
}

var cbAccountPaths = [][]string{
	{"data", "Response", "Data", "Accounts"},
	{"data", "data", "Response", "Data", "Accounts"},
	{"Response", "Data", "Accounts"},
	{"data", "resources"},
	{"data", "data", "resources"},
}

// cbEnterpriseUsage 是企业成员账号的周期用量。
type cbEnterpriseUsage struct {
	LimitNum        cbNum  `json:"limitNum"`
	LimitNumSnake   cbNum  `json:"limit_num"`
	UsedNum         cbNum  `json:"usedNum"`
	UsedNumSnake    cbNum  `json:"used_num"`
	Credit          cbNum  `json:"credit"`
	CycleStartTime  cbTime `json:"cycleStartTime"`
	CycleStartSnake cbTime `json:"cycle_start_time"`
	CycleEndTime    cbTime `json:"cycleEndTime"`
	CycleEndSnake   cbTime `json:"cycle_end_time"`
	CycleResetTime  cbTime `json:"cycleResetTime"`
	CycleResetSnake cbTime `json:"cycle_reset_time"`
}

// cbEnterpriseFromBody 兼容 data / data.data 两层包裹（不同站点不同）。
func cbEnterpriseFromBody(body []byte) (*cbEnterpriseUsage, bool) {
	var root any
	if json.Unmarshal(body, &root) != nil {
		return nil, false
	}
	node := root
	for _, path := range [][]string{{"data", "data"}, {"data"}} {
		if v, ok := cbWalk(node, path); ok {
			if _, isObject := v.(map[string]any); isObject {
				node = v
				break
			}
		}
	}
	raw, err := json.Marshal(node)
	if err != nil {
		return nil, false
	}
	var usage cbEnterpriseUsage
	if json.Unmarshal(raw, &usage) != nil {
		return nil, false
	}
	return &usage, true
}

/* ---------------- 聚合 ---------------- */

// cbPackage 是一个聚合后的额度段（同一 PackageCode 的分页条目已合并）。
type cbPackage struct {
	Code    string
	Name    string
	Total   float64
	Remain  float64
	Reset   int64
	IsExtra bool
}

// cbAggregatePackages 按 PackageCode 分组合并。
//
// 为什么必须分组：一个账号的资源包里，主套餐、赠送包、活动包、加量包
// 是各自独立的额度池，容量不能相加；只有同一个 PackageCode 的多个分页
// 条目才是"同一份额度"，可以求和。
func cbAggregatePackages(accounts []cbResource) []cbPackage {
	order := make([]string, 0, len(accounts))
	byCode := make(map[string]*cbPackage, len(accounts))
	for _, account := range accounts {
		// Status 允许 0（有效）与 3（待生效）；其它状态（过期/停用）不参与读数。
		if account.Status.OK && account.Status.Value != 0 && account.Status.Value != 3 {
			continue
		}
		total, remain, ok := account.cbTotals()
		if !ok {
			continue
		}
		code := account.PackageCode
		if code == "" {
			code = account.PackageName
		}
		if code == "" {
			code = "package"
		}
		pkg, exists := byCode[code]
		if !exists {
			pkg = &cbPackage{Code: code, Name: account.PackageName}
			byCode[code] = pkg
			order = append(order, code)
		}
		pkg.Total += total
		pkg.Remain += remain
		pkg.Reset = cbEarliest(pkg.Reset, account.cbReset())
	}
	out := make([]cbPackage, 0, len(order))
	for _, code := range order {
		pkg := byCode[code]
		pkg.IsExtra = code == cbExtraPackageCode
		out = append(out, *pkg)
	}
	return out
}

func cbSplitPackages(pkgs []cbPackage) (main, extra []cbPackage) {
	for _, pkg := range pkgs {
		if pkg.IsExtra {
			extra = append(extra, pkg)
			continue
		}
		main = append(main, pkg)
	}
	return main, extra
}

func cbSumTotal(pkgs []cbPackage) float64 {
	var sum float64
	for _, pkg := range pkgs {
		sum += pkg.Total
	}
	return sum
}

/* ---------------- 基础类型与工具 ---------------- */

// cbNum 兼容 number / 数字字符串，并保留"字段是否存在"。
//
// 不能用 float64 直接接：0 是合法额度（本周期刚好用完），
// 拿 0 当"没数据"会把"已用完"显示成"还有额度"。
type cbNum struct {
	Value float64
	OK    bool
}

func (n *cbNum) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var text string
		if err := json.Unmarshal(b, &text); err != nil {
			return nil
		}
		s = strings.TrimSpace(text)
		if s == "" {
			return nil
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil // 未知形态按"缺失"处理，不让一个展示字段拖垮整次读数
	}
	n.Value, n.OK = v, true
	return nil
}

// cbTime 兼容 RFC3339 字符串、"2006-01-02 15:04:05" 与秒/毫秒数字。
//
// 不能直接当数字接：上游多数版本给的是 RFC3339 字符串，
// 按数字解会变 0，重置时间就从界面上消失。
type cbTime struct {
	Text  string
	Num   float64
	IsNum bool
	OK    bool
}

func (t *cbTime) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" || s == `""` {
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var text string
		if err := json.Unmarshal(b, &text); err != nil {
			return nil
		}
		t.Text, t.OK = strings.TrimSpace(text), true
		return nil
	}
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		t.Num, t.IsNum, t.OK = v, true, true
	}
	return nil
}

// Unix 返回 Unix 秒；解析不出返回 0（调用方按"没有重置时间"处理）。
func (t cbTime) Unix() int64 {
	if !t.OK {
		return 0
	}
	if !t.IsNum {
		// 数字也可能被写成字符串（"1764547200"）。
		if v, err := strconv.ParseFloat(t.Text, 64); err == nil {
			t.Num, t.IsNum = v, true
		}
	}
	if t.IsNum {
		v := t.Num
		if v > 1e12 { // 毫秒
			v /= 1000
		}
		if v <= 0 {
			return 0
		}
		return int64(v)
	}
	for _, layout := range []string{
		time.RFC3339Nano, time.RFC3339,
		"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02",
	} {
		if ts, err := time.ParseInLocation(layout, t.Text, time.Local); err == nil {
			return ts.Unix()
		}
	}
	return 0
}

func cbRawField(raw map[string]json.RawMessage, keys ...string) json.RawMessage {
	for _, key := range keys {
		if v, ok := raw[key]; ok {
			return v
		}
	}
	for _, key := range keys {
		for k, v := range raw {
			if strings.EqualFold(k, key) {
				return v
			}
		}
	}
	return nil
}

func cbStringField(raw map[string]json.RawMessage, keys ...string) string {
	v := cbRawField(raw, keys...)
	if v == nil {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

func cbNumFrom(raw json.RawMessage) (n cbNum) {
	if raw == nil {
		return n
	}
	_ = json.Unmarshal(raw, &n)
	return n
}

func cbTimeFrom(raw json.RawMessage) (t cbTime) {
	if raw == nil {
		return t
	}
	_ = json.Unmarshal(raw, &t)
	return t
}

func cbFirstNum(values ...cbNum) cbNum {
	for _, v := range values {
		if v.OK {
			return v
		}
	}
	return cbNum{}
}

func cbFirstTime(values ...cbTime) cbTime {
	for _, v := range values {
		if v.OK {
			return v
		}
	}
	return cbTime{}
}

// cbWalk 按路径取嵌套值；找不到返回 false。
func cbWalk(root any, path []string) (any, bool) {
	current := root
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := object[key]
		if !ok {
			// 键名大小写放宽：CN / 国际站的 JSON 偶有差异。
			for k, v := range object {
				if strings.EqualFold(k, key) {
					next, ok = v, true
					break
				}
			}
			if !ok {
				return nil, false
			}
		}
		current = next
	}
	return current, true
}

func cbEarliest(current, candidate int64) int64 {
	if candidate <= 0 {
		return current
	}
	if current == 0 || candidate < current {
		return candidate
	}
	return current
}

func cbClamp(v, low, high float64) float64 {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func cbPercent(used, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return cbClamp(used/total*100, 0, 100)
}

func cbRound(v float64) int64 {
	if v <= 0 {
		return 0
	}
	return int64(math.Round(v))
}
