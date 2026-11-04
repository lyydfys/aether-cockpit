package gateway

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// S3 兼容对象存储的同步后端（AWS S3 / Cloudflare R2 / MinIO / B2）。
// 只做 PUT / GET 单个对象，用 SigV4 签名；不引 SDK，保持单 exe。

const s3EmptyPayloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// s3Target 描述一次对象地址。
type s3Target struct {
	URL          string // 完整对象 URL
	CanonicalURI string // 参与签名的路径（已编码）
	Host         string
}

func s3Key(cfg *SyncS3Config, filename string) string {
	prefix := strings.Trim(cfg.Prefix, "/")
	if prefix == "" {
		return filename
	}
	return prefix + "/" + filename
}

func s3ObjectTarget(cfg *SyncS3Config, key string) (*s3Target, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("S3 需要 bucket")
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	scheme, host := "https", "s3."+region+".amazonaws.com"
	if cfg.Endpoint != "" {
		u, err := url.Parse(cfg.Endpoint)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("S3 Endpoint 无效: %s", cfg.Endpoint)
		}
		scheme, host = u.Scheme, u.Host
		if scheme == "" {
			scheme = "https"
		}
	}
	escapedKey := escapeS3Path(key)
	var objectURL, canonical, objectHost string
	if cfg.PathStyle {
		objectHost = host
		canonical = "/" + cfg.Bucket + "/" + escapedKey
		objectURL = scheme + "://" + host + canonical
	} else {
		objectHost = cfg.Bucket + "." + host
		canonical = "/" + escapedKey
		objectURL = scheme + "://" + objectHost + canonical
	}
	return &s3Target{URL: objectURL, CanonicalURI: canonical, Host: objectHost}, nil
}

// escapeS3Path 逐段编码，保留 "/"（SigV4 要求）。
func escapeS3Path(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// signV4 计算 Authorization 头。headers 必须包含 host、x-amz-date、
// x-amz-content-sha256（以及任何额外已签名的头）。
func signV4(method, canonicalURI, canonicalQuery string, headers map[string]string, payloadHash, region, accessKey, secretKey string, now time.Time) string {
	lower := make(map[string]string, len(headers))
	keys := make([]string, 0, len(headers))
	for k, v := range headers {
		lk := strings.ToLower(strings.TrimSpace(k))
		lower[lk] = strings.TrimSpace(v)
		keys = append(keys, lk)
	}
	sort.Strings(keys)
	var canonicalHeaders strings.Builder
	for _, k := range keys {
		canonicalHeaders.WriteString(k + ":" + lower[k] + "\n")
	}
	signedHeaders := strings.Join(keys, ";")

	canonicalRequest := strings.Join([]string{
		method, canonicalURI, canonicalQuery, canonicalHeaders.String(), signedHeaders, payloadHash,
	}, "\n")

	amzDate := now.UTC().Format("20060102T150405Z")
	date := amzDate[:8]
	scope := date + "/" + region + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])

	kDate := hmacSHA256([]byte("AWS4"+secretKey), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, "s3")
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	return "AWS4-HMAC-SHA256 Credential=" + accessKey + "/" + scope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + signature
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func s3PutObject(cfg *SyncS3Config, key string, data []byte) (string, error) {
	target, err := s3ObjectTarget(cfg, key)
	if err != nil {
		return "", err
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	sum := sha256.Sum256(data)
	payloadHash := hex.EncodeToString(sum[:])
	now := time.Now()
	amzDate := now.UTC().Format("20060102T150405Z")
	headers := map[string]string{
		"host":                 target.Host,
		"x-amz-content-sha256": payloadHash,
		"x-amz-date":           amzDate,
	}
	auth := signV4(http.MethodPut, target.CanonicalURI, "", headers, payloadHash, region, cfg.AccessKey, cfg.SecretKey, now)

	req, err := http.NewRequest(http.MethodPut, target.URL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("S3 上传失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return "", fmt.Errorf("S3 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return target.URL, nil
}

func s3GetObject(cfg *SyncS3Config, key string) ([]byte, string, error) {
	target, err := s3ObjectTarget(cfg, key)
	if err != nil {
		return nil, "", err
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	now := time.Now()
	amzDate := now.UTC().Format("20060102T150405Z")
	headers := map[string]string{
		"host":                 target.Host,
		"x-amz-content-sha256": s3EmptyPayloadHash,
		"x-amz-date":           amzDate,
	}
	auth := signV4(http.MethodGet, target.CanonicalURI, "", headers, s3EmptyPayloadHash, region, cfg.AccessKey, cfg.SecretKey, now)

	req, err := http.NewRequest(http.MethodGet, target.URL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("x-amz-content-sha256", s3EmptyPayloadHash)
	req.Header.Set("x-amz-date", amzDate)

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("S3 下载失败: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode == http.StatusNotFound {
		// 明确的"远端还没有这个对象"：与"读失败"必须区分，
		// 否则一次超时就会被当成不存在，用本机整包覆盖另一台机器。
		return nil, "", errSyncRemoteAbsent
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("S3 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body[:minInt(len(body), 300)])))
	}
	return body, target.URL, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
