# 部署到云服务器

站点是**零构建静态页**：没有 Node 运行时、没有数据库、不需要反向代理到后端。
两套部署方式，按服务器情况选：

| 方式 | 适用 |
|---|---|
| **Docker（推荐）** | 服务器是 Docker 镜像（如本项目的 `Docker-pquk`） |
| 宿主机直装 nginx | 服务器是普通 Ubuntu/Debian |

---

## 零、先设站点地址（两种方式都要做）

`index.html` 里有 4 处绝对地址（canonical、og:url、og:image、twitter:image）。
不改的话分享卡会指向旧地址，搜索引擎也会认错规范链接。

```bash
# 有域名
python scripts/set-site-url.py https://example.com

# 暂时只有公网 IP
python scripts/set-site-url.py http://43.108.89.68
```

改完用 `git diff website/index.html` 确认只动了这几行。

> **纯 IP 访问没有 HTTPS**，分享卡在多数平台不会展开预览图。
> 想要完整效果，最终还是要绑一个域名。

---

## 一、Docker 部署（推荐）

### 1. 把站点传到服务器

```bash
# 本机执行（把 website/ 传上去，构建需要 deploy/ 与源码）
scp -r website user@43.108.89.68:~/aether-cockpit
```

也可以用打包脚本只传运行时文件 + deploy 目录：

```bash
python scripts/package.py
scp dist/aether-cockpit-site.tar.gz user@43.108.89.68:/tmp/
```

### 2. 构建并启动

```bash
cd ~/aether-cockpit
docker compose -f deploy/docker-compose.yml up -d --build
```

**如果服务器没装 compose 插件**（报 `docker: 'compose' is not a docker command`），
用等价的原生命令：

```bash
cd ~/aether-cockpit
docker build -f deploy/Dockerfile -t aether-cockpit-site:latest .
docker run -d \
  --name aether-cockpit-site \
  --restart unless-stopped \
  -p 80:80 \
  --read-only \
  --tmpfs /var/cache/nginx \
  --tmpfs /var/run \
  --tmpfs /tmp \
  --memory 128m \
  --log-opt max-size=10m \
  --log-opt max-file=3 \
  aether-cockpit-site:latest
```

这串参数与 `docker-compose.yml` 完全等价，任选一种即可。

### 3. 确认

```bash
docker ps                              # 容器应为 Up (healthy)
docker logs aether-cockpit-site        # 看 nginx 日志
curl -I http://localhost/              # 本机自测
```

浏览器打开 `http://43.108.89.68/` 即可。

### 常用命令

```bash
docker compose -f deploy/docker-compose.yml logs -f    # 跟日志
docker compose -f deploy/docker-compose.yml restart    # 重启
docker compose -f deploy/docker-compose.yml down       # 停止并移除
```

### 更新站点

```bash
# 本机：改完站点后重新传
scp -r website user@43.108.89.68:~/aether-cockpit

# 服务器：重建镜像并滚动重启
cd ~/aether-cockpit
docker compose -f deploy/docker-compose.yml up -d --build
```

`docker-compose.yml` 里做了几件事，知道为什么：

- `read_only: true` —— 静态站不需要写盘，被入侵时少一条落地路径；
  另配了 `tmpfs` 给 nginx 的缓存与 pid，否则起不来。
- `restart: unless-stopped` —— 服务器重启后自动拉起。
- 日志 `max-size: 10m` x3 —— 40 GiB 盘不至于被日志吃满。
- `memory: 128M` —— 2 GiB 的小机器上给个上限。

---

## 二、宿主机直装 nginx

```bash
python scripts/package.py
scp dist/aether-cockpit-site.tar.gz user@host:/tmp/

# 服务器
sudo mkdir -p /var/www/aether-cockpit
sudo tar -xzf /tmp/aether-cockpit-site.tar.gz -C /var/www/aether-cockpit
sudo chown -R www-data:www-data /var/www/aether-cockpit
sudo find /var/www/aether-cockpit -type d -exec chmod 755 {} \;
sudo find /var/www/aether-cockpit -type f -exec chmod 644 {} \;

sudo cp deploy/aether-cockpit.conf /etc/nginx/sites-available/
sudo ln -s /etc/nginx/sites-available/aether-cockpit.conf /etc/nginx/sites-enabled/
sudo nano /etc/nginx/sites-available/aether-cockpit.conf   # 改 server_name 与 root
sudo nginx -t && sudo systemctl reload nginx
```

---

## 三、云平台必须做的一步：放行端口

**这一步最容易漏，表现是「服务明明起来了，外网就是打不开」。**

到云控制台的**安全组 / 防火墙**里放行入方向：

| 端口 | 用途 |
|---|---|
| 80/tcp | HTTP |
| 443/tcp | HTTPS |

只放行 80/443，**不要**把 22 以外的端口全开。

放行后用 `curl -I http://你的公网IP/` 从**外部**验证
（在服务器上 curl localhost 成功不代表外网可达）。

---

## 四、HTTPS

### 有域名

```bash
# 宿主机装 nginx 的情况
sudo certbot --nginx -d example.com -d www.example.com

# Docker 的情况：让宿主机 nginx 或 Caddy 反代到容器的 80 端口，
# 证书由外层处理；或者直接用 Caddy 替代本项目的 nginx 容器。
```

HSTS 建议**跑稳一周后再开**，开了浏览器会记住，回退很麻烦。

### 只有 IP

没有域名签不了证书。三条路：

1. 绑一个域名（最省事，推荐）；
2. 用 Cloudflare 免费证书 + 代理；
3. 接受纯 HTTP。

---

## 五、验证清单

```bash
# 状态码与缓存头
curl -I http://你的地址/
curl -I http://你的地址/assets/css/site.css
curl -I http://你的地址/assets/img/shots/m-quota.webp

# 字体 Content-Type 必须是 font/woff2（nginx < 1.21.1 会漏，见下）
curl -I http://你的地址/assets/fonts/noto-sans-sc-400.subset.woff2

# gzip 是否生效
curl -sS -H 'Accept-Encoding: gzip' -o /dev/null -w '%{size_download}\n' http://你的地址/

# 404
curl -s http://你的地址/nope | head -3
```

---

## 缓存为什么不是「一年 immutable」

前端常见做法是文件名加内容哈希再设一年缓存。本站**没有构建步骤、文件名固定**，
照搬会导致「换了截图，老访客一直看旧的，而且没补救办法」。

当前策略按更新频率分档：

| 资源 | 缓存 |
|---|---|
| `index.html` | `no-cache`，每次校验 |
| CSS / JS | 1 小时 |
| 图片 / 字体 | 30 天 |

想要长缓存：引入构建加哈希，或用 `?v=20261108` 这类版本参数。

---

## 两个已经踩过的坑

**1. `types{}` 块会整体替换 MIME 表。**
在 `server` 里写 `types {...}` 不是「追加」而是「替换」，
没列出的类型全部退化成 `application/octet-stream`。
表现为 `og-cover.jpg` 被抓取器当成二进制文件，分享卡不显示图。

修法：删掉那个块，用 nginx 自带 `mime.types`；只为字体单独开一个
`location` 并在其内部声明 `types`（该 location 只服务字体，替换是安全的）。

**2. nginx < 1.21.1 的 `mime.types` 没有 woff2。**
表现为字体 `Content-Type: application/octet-stream`。
字体通常仍能加载，但部分浏览器和 CDN 会拒绝或告警。
本仓库的配置已针对字体单独声明，无需改系统 `mime.types`。

---

## 备选：GitHub Pages

仓库里有 `.github/workflows/site.yml`，会发布 `website/` 到 Pages。
用云服务器的话这个工作流保持关闭即可，不影响服务器部署。
不需要的话可以删掉该文件。
