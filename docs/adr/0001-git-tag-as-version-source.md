# 版本号以 git tag 为唯一来源

发布流程用 `git tag vX.Y.Z` 作为版本的唯一来源：CI 用 `-ldflags "-X main.Version=X.Y.Z"` 注入二进制，用同一个 tag 重写 `build/config.yml` 的 `info.version`，再由 `wails3 update build-assets` 派生出 exe VERSIONINFO、NSIS `VIProductVersion`、`Info.plist` 与 deb/rpm 的版本号。此前版本号散落在四处手写（`main.go`、`build/config.yml`、`build/windows/info.json`、`build/linux/nfpm/nfpm.yaml`），实际已漂移为「应用自称 0.4.0、Windows 安装包元数据与 deb 都是 0.1.0」，而且生成器会覆盖手改的产物，所以「多副本 + 人工同步」在结构上不可维护。

## 后果

- 构建不带 tag 时版本为 `dev`，semver 比较下永远「更旧」，因此开发构建一定会被提示升级——这是有意的，避免 `dev` 被误判为最新。
- 发布前必须先打 tag 再构建；本地 `wails3 build` 得到的是 `dev`，不能拿来发布。
- `main.go` 的 `Version` 必须是 `var`：`const` 会被 `-X` 静默忽略，`scripts/set-version.sh` 会主动拒绝这种写法。
