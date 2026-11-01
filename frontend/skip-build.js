// ui2 是 //go:embed 的原生 ES module，没有构建步骤。
// Wails 会在 frontend:dir 里执行 frontend:install/build，这里用 no-op 兜住。
console.log("skip frontend build: ui2 is served from internal/web/ui2");
