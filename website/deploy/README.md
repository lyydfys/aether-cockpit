# 部署到云服务器

站点是**零构建静态页**：没有 Node 运行时、没有数据库、不需要反向代理到后端。
把文件放到 nginx 的 `root` 目录即可。

## 一、先设站点地址（重要）

`index.html` 里有 4 处绝对地址（canonical、og:url、og:image、twitter:image）。
不改的话，分享卡会指向旧地址，搜索引擎也会认错规范链接。

```bash
# 域名部署在根路径
python scripts/set-site-url.py https://example.com

# 如果是子路径
python scripts/set-site-url.py https://example.com/gateway/
```

改完可以用 `git diff website/index.html` 确认只动了这几行。

## 二、打包

```bash
python scripts/package.py
```

产出 `dist/aether-cockpit-site.tar.gz`，只含运行时文件
（`index.html`、`404.html`、`assets/`），开发脚本与 Markdown 不进包。

脚本会自动校验：`index.html` 里引用的本地资源必须都在包内，缺一个就报错。

## 三、上传并解包

```bash
# 从本地推到服务器
scp dist/aether-cockpit-site.tar.gz user@your-server:/tmp/

# 服务器上
sudo mkdir -p /var/www/aether-cockpit
sudo tar -xzf /tmp/aether-cockpit-site.tar.gz -C /var/www/aether-cockpit
sudo chown -R www-data:www-data /var/www/aether-cockpit
sudo find /var/www/aether-cockpit -type d -exec chmod 755 {} \;
sudo find /var/www/aether-cockpit -type f -exec chmod 644 {} \;
```

## 四、配置 nginx

```bash
sudo cp deploy/aether-cockpit.conf /etc/nginx/sites-available/
sudo ln -s /etc/nginx/sites-available/aether-cockpit.conf /etc/nginx/sites-enabled/
sudo nano /etc/nginx/sites-available/aether-cockpit.conf   # 改 server_name 和 root
sudo nginx -t
sudo systemctl reload nginx
```

配置文件里只有三处需要改，都标了「改这里」。

## 五、HTTPS

```bash
sudo certbot --nginx -d example.com -d www.example.com
```

签好后回到配置文件，把 80 端口的 `return 301` 打开，并启用 `443` 那段。
HSTS 建议**跑稳一周后再开**，开了浏览器会记住，回退很麻烦。

## 六、验证

```bash
curl -I https://example.com/                       # 看 200 与 Cache-Control
curl -I https://example.com/assets/css/site.css    # 看 gzip 与 max-age
curl -I https://example.com/assets/img/og-cover.png
curl -s https://example.com/nope | head -3         # 应返回 404 页面
```

浏览器里再用手机和桌面各看一遍，确认开屏动效与主题切换正常。

---

## 缓存为什么不是「一年 immutable」

前端工程常见的做法是给文件名加内容哈希（`site.a1b2c3.css`）然后设一年缓存。
本站没有构建步骤，文件名固定，所以**不能**这么做 —— 否则你换了截图或样式，
老访客会一直看到旧文件，且无法用改文件名的方式补救。

当前策略按更新频率分档：

| 资源 | 缓存 |
|---|---|
| `index.html` | `no-cache`，每次都校验 |
| CSS / JS | 1 小时 |
| 图片 / 字体 | 30 天 |

如果以后想要长缓存，两条路：
1. 引入构建步骤，给文件名加哈希；
2. 在链接后加版本参数（`site.css?v=20261108`），改版时手动更新。

## 更新站点

```bash
python scripts/set-site-url.py https://example.com   # 若地址没变可跳过
python scripts/package.py
scp dist/aether-cockpit-site.tar.gz user@host:/tmp/
ssh user@host 'sudo tar -xzf /tmp/aether-cockpit-site.tar.gz -C /var/www/aether-cockpit'
```

`index.html` 是 `no-cache`，所以发完立刻生效；CSS/JS 最多 1 小时后过期，
急着让所有人看到新样式可以用 `?v=` 参数强制刷新。

## 备选：GitHub Pages

仓库里有 `.github/workflows/site.yml`，会发布 `website/` 到 Pages。
用云服务器的话这个工作流可以删掉，或在仓库 Settings → Pages 里保持关闭即可，
它不会影响服务器部署。
