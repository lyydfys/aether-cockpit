# 构建镜像：在 website/ 目录下执行
#   docker build -f deploy/Dockerfile -t aether-cockpit-site:latest .
#
# 构建上下文是 website/ 根目录，所以这里能 COPY 到 index.html 与 assets/。

FROM nginx:1.27-alpine

# 站点文件
COPY index.html /usr/share/nginx/html/index.html
COPY 404.html   /usr/share/nginx/html/404.html
COPY assets/    /usr/share/nginx/html/assets/

# 容器内 nginx 配置
COPY deploy/nginx-container.conf /etc/nginx/conf.d/default.conf

# 默认配置里那个 50x 页面用不上，去掉减少混淆
RUN rm -f /usr/share/nginx/html/index.nginx-debian.html

EXPOSE 80

# 静态站无需健康检查脚本，用 TCP 探测即可（compose 里声明）
CMD ["nginx", "-g", "daemon off;"]
