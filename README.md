# BeaconDesk

BeaconDesk 是一个开源远程协助项目，目标是实现类似 AnyDesk / 向日葵的远程管理体验，但只面向合法、透明、用户明确授权的协助场景。

本项目明确不实现隐藏运行、静默控制、绕过系统权限、逃避安全软件、窃取凭据或侵犯隐私等能力。

## 当前能力

- Go 编写的中转/连接服务器，支持 TCP JSON Lines 和 WebSocket。
- Windows 客户端使用 Gio 自绘 UI，无需 Wails、浏览器内核或 Web 前端运行时。
- 支持无界面 headless 模式，便于联调和烟雾测试。
- 自动生成唯一设备 ID。
- 设备注册支持共享 token。
- 支持 6 位临时验证码。
- 心跳 ping/pong、延迟显示、断线重连、服务端空闲连接清理。
- 会话请求、确认、就绪、关闭等基础信令。
- 支持模拟帧转发。
- Windows GDI 屏幕采集和 JPEG 画面帧转发。
- 静态画面检测，未变化画面降频发送。
- 动态 JPEG 质量和 FPS 调整。
- 码率、丢包率、帧数、重连次数显示。
- 服务端 token bucket 限速。
- 被控端 GUI 必须明确允许后才建立会话。
- 支持可信设备记住/撤销。
- 服务端强制校验会话模式和输入权限。
- 支持观看模式和观看并控制模式。
- 本地审计日志记录请求、授权、会话和输入事件。
- 支持显式授权后的鼠标键盘输入。
- Windows exe 图标资源。
- 原生 TLS 监听支持。
- Windows 客户端支持从 GitHub Releases 检查更新，只提示，不静默下载或替换。
- 中转服务器支持网页端控制页面。
- Windows 被控端可生成网页控制链接，查看有效期，复制、打开、刷新、撤销。
- Linux 安装脚本支持校验 Release 的 SHA256SUMS。

当前 MVP 可用明文 TCP 做本地测试。公网部署建议使用 WebSocket + TLS，或在反向代理后启用 TLS。Windows 客户端默认按 WebSocket + TLS 思路配置；关闭 TLS 只建议本地测试。

## 项目结构

```text
cmd/client-windows      Windows 客户端和 headless 测试入口
cmd/relay-server        Linux 中转/连接服务器入口
internal/protocol       通信信封和 payload 定义
internal/auth           设备身份、token、临时验证码
internal/transport      TCP / WebSocket 传输抽象
internal/relay          服务端设备、会话、网页控制路由
internal/client         GUI 和 CLI 共用的客户端运行时
internal/desktop        屏幕采集、缩放、编码、变化检测
internal/input          鼠标键盘事件定义和注入边界
internal/config         服务端配置加载与默认配置
scripts                 Linux 安装、卸载、systemd 文件
configs                 示例配置文件
assets/brand            Logo 和图标资源
```

## 本地运行

启动中转服务器：

```bash
go run ./cmd/relay-server -config configs/relay.example.conf
```

`configs/relay.example.conf` 是开发测试配置。公网部署必须配置 TLS，并把 `allow_insecure_plaintext` 设为 `false`。

WebSocket 模式示例：

```text
listen = ":8443"
transport = "websocket"
websocket_path = "/ws"
web_control_enabled = true
web_control_path = "/web"
shared_token = "change-me"
```

TLS 配置示例：

```text
tls_cert_file = "/etc/beacondesk/tls/fullchain.pem"
tls_key_file = "/etc/beacondesk/tls/privkey.pem"
allow_insecure_plaintext = false
```

安装脚本默认会生成自签名证书，所以 Windows 客户端如果开启证书校验，可能提示 `certificate signed by unknown authority`。推荐方案是给中转服务器配置域名和 Let’s Encrypt 等可信证书。临时使用自签证书时，不建议长期打开“跳过 TLS 证书校验”，可以在服务器查看证书 SHA256 指纹：

```bash
curl -fsSL https://raw.githubusercontent.com/poouo/BeaconDesk/main/scripts/install.sh | sudo sh -s -- fingerprint
```

然后在 Windows 客户端“设置 -> 连接 -> TLS 证书指纹”中填入该值，保持“使用 TLS”开启，并关闭“跳过 TLS 证书校验”。

网页控制链接由服务端提供。在 WebSocket 模式下，`/web` 和 `/ws` 复用同一个 HTTP 监听端口。TCP 模式下可以额外开启：

```text
web_listen = ":8080"
public_base_url = "https://relay.example.com"
```

`public_base_url` 用于生成可发给他人的公网链接。没有配置时，服务端会根据当前监听地址生成本地链接。

## Headless 测试

启动被控端：

```bash
go run ./cmd/client-windows -headless -server 127.0.0.1:8443 -name controlled -role controlled -auto-accept -mock-frames -token change-me
```

发布临时验证码：

```bash
go run ./cmd/client-windows -headless -server 127.0.0.1:8443 -name controlled -role controlled -publish-code -mock-frames -token change-me
```

临时验证码有效期 10 分钟，只保存在服务端内存中，并在一次成功请求后消费。

启动控制端：

```bash
go run ./cmd/client-windows -headless -server 127.0.0.1:8443 -name controller -role controller -target dev_xxx -token change-me
```

如果目标设备发布了验证码：

```bash
go run ./cmd/client-windows -headless -server 127.0.0.1:8443 -name controller -role controller -target dev_xxx -target-code 123456 -token change-me
```

启用真实屏幕帧：

```bash
go run ./cmd/client-windows -headless -server 127.0.0.1:8443 -name controlled -role controlled -auto-accept -screen-frames -capture-fps 2 -capture-width 1280 -capture-height 720 -capture-quality 55 -token change-me
```

低带宽参数示例：

```bash
go run ./cmd/client-windows -headless -server 127.0.0.1:8443 -name controlled -role controlled -screen-frames -capture-fps 2 -bandwidth-limit-kbps 1024 -static-frame-seconds 8 -token change-me
```

`-auto-accept` 仅用于本地 MVP 测试。正式使用时应由被控端用户在 GUI 中手动允许或拒绝。

## Windows 客户端

构建 Windows GUI 客户端：

```powershell
go build -tags "desktop,production" -ldflags "-H windowsgui" -o dist\beacondesk-client-windows-amd64.exe .\cmd\client-windows
```

输出文件：

```text
dist\beacondesk-client-windows-amd64.exe
```

客户端默认中文，可在设置中切换中英文。主界面只保留常用操作；服务器地址、传输方式、TLS、设备名、token、角色、请求模式、授权策略、可信设备、审计日志、更新检查、屏幕发送、帧率、画质、分辨率、码率等都放在“设置”窗口中。

设置保存到当前工作目录的：

```text
beacondesk-client.json
```

可信设备和审计日志保存在当前用户配置目录下的 `BeaconDesk` 目录中。请保护系统账户和配置目录权限。

### 网页控制

被控端在 Windows 客户端中进入“设置 -> 网页控制”：

1. 设置链接有效期。
2. 点击生成链接。
3. 复制链接并发给协助者。
4. 协助者在浏览器打开链接。
5. 被控端收到请求后必须点击允许。
6. 被控端可随时撤销链接。

链接本身不等于免授权控制。每次网页访问仍会显示为被控端本机的连接请求；被控端未允许前，浏览器不能看到画面，也不能发送输入。

服务端不会提供公开在线设备列表。网页控制链接只指向生成它的那台被控设备。

## Linux 一键安装

安装命令：

```bash
curl -fsSL https://raw.githubusercontent.com/poouo/BeaconDesk/main/scripts/install.sh | sudo sh -s -- install
```

服务管理：

```bash
curl -fsSL https://raw.githubusercontent.com/poouo/BeaconDesk/main/scripts/install.sh | sudo sh -s -- start
curl -fsSL https://raw.githubusercontent.com/poouo/BeaconDesk/main/scripts/install.sh | sudo sh -s -- stop
curl -fsSL https://raw.githubusercontent.com/poouo/BeaconDesk/main/scripts/install.sh | sudo sh -s -- restart
curl -fsSL https://raw.githubusercontent.com/poouo/BeaconDesk/main/scripts/install.sh | sudo sh -s -- status
```

查看当前 TLS 证书 SHA256 指纹：

```bash
curl -fsSL https://raw.githubusercontent.com/poouo/BeaconDesk/main/scripts/install.sh | sudo sh -s -- fingerprint
```

升级：

```bash
curl -fsSL https://raw.githubusercontent.com/poouo/BeaconDesk/main/scripts/install.sh | sudo sh -s -- upgrade
```

卸载：

```bash
curl -fsSL https://raw.githubusercontent.com/poouo/BeaconDesk/main/scripts/install.sh | sudo sh -s -- uninstall
```

默认配置文件：

```text
/etc/beacondesk/relay.conf
```

systemd 服务：

```text
beacondesk-relay.service
```

### 关于 404

安装脚本默认从 GitHub Release 下载这些文件：

```text
beacondesk-relay-linux-amd64
beacondesk-relay-linux-arm64
beacondesk-relay-linux-armv7
SHA256SUMS
```

如果仓库还没有创建 Release，GitHub 的 `latest/download/...` 会返回 404。解决方式：

1. 打 tag，例如 `v0.1.0`。
2. 推送 tag 到 GitHub。
3. 等 GitHub Actions 的 `release` workflow 构建并发布资产。

```powershell
git tag v0.1.0
git push origin v0.1.0
```

没有 Release 时，也可以在服务器安装 Go 后使用源码构建兜底模式：

```bash
curl -fsSL https://raw.githubusercontent.com/poouo/BeaconDesk/main/scripts/install.sh | sudo BEACONDESK_BUILD_FROM_SOURCE=1 sh -s -- install
```

## 服务器主机名提示

如果看到：

```text
sudo: unable to resolve host hkva424381: Name or service not known
```

这是服务器 `/etc/hostname` 和 `/etc/hosts` 不匹配导致的 sudo 提示，不是 BeaconDesk 安装脚本的问题。通常可检查：

```bash
hostname
cat /etc/hostname
cat /etc/hosts
```

确保 `/etc/hosts` 中有类似：

```text
127.0.1.1 hkva424381
```

## 品牌资源

项目名：BeaconDesk。

图标和 Logo 位于：

```text
assets/brand
```

重新生成图标：

```bash
node tools/render_icon.js
go run github.com/akavel/rsrc@latest -ico cmd/client-windows/build/windows/icon.ico -o cmd/client-windows/rsrc_windows_amd64.syso -arch amd64
```

## 安全说明

- 所有远程控制都必须经过被控端明确确认，或使用可见、可撤销的预配置授权。
- 临时验证码只做短期校验，不替代被控端确认。
- 鼠标键盘输入默认关闭，必须由被控端明确启用并授权。
- 服务端会校验会话模式，不允许未授权输入。
- 可信设备功能可见、可撤销，不是隐藏无人值守控制。
- 网页控制链接有有效期、可撤销，并且只绑定生成它的设备。
- 服务端不公开在线设备目录。
- 本地审计日志只记录协议元数据，不记录截图内容和键盘文本。
- headless `-auto-accept` 只用于本地测试，不建议生产环境使用。
- 设备身份和 token 属于敏感信息，不要提交到仓库。
- 公网部署必须启用 TLS、强 token、防火墙和监控。

## 路线图

- 更可靠的 DXGI Desktop Duplication 采集。
- 审计日志导出和筛选。
- 更强的 Windows 授权弹窗。
- 更完善的拥塞控制和变化区域编码。
- QUIC 或 WebRTC 传输。
- 端到端加密和账号/设备管理。
- 带账号认证的网页控制台，用于安全选择设备。
