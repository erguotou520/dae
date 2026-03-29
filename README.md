# dae

<img src="https://github.com/daeuniverse/dae/blob/main/logo.png" border="0" width="25%">

<p align="left">
    <img src="https://github.com/daeuniverse/dae/actions/workflows/build.yml/badge.svg" alt="Build"/>
    <img src="https://custom-icon-badges.herokuapp.com/github/license/daeuniverse/dae?logo=law&color=orange" alt="License"/>
    <img src="https://custom-icon-badges.herokuapp.com/github/v/release/daeuniverse/dae?logo=rocket" alt="version">
    <img src="https://custom-icon-badges.herokuapp.com/github/issues-pr-closed/daeuniverse/dae?color=purple&logo=git-pull-request&logoColor=white"/>
    <img src="https://custom-icon-badges.herokuapp.com/github/last-commit/daeuniverse/dae?logo=history&logoColor=white" alt="lastcommit"/>
</p>

**_dae_**, means goose, is a high-performance transparent proxy solution.

To enhance traffic split performance as much as possible, dae employs the transparent proxy and traffic split suite within the Linux kernel using eBPF. As a result, dae can enable direct traffic to bypass the proxy application's forwarding, facilitating genuine direct traffic passage. Through this remarkable feat, there is minimal performance loss and negligible additional resource consumption for direct traffic.

As a successor of [v2rayA](https://github.com/v2rayA/v2rayA), dae abandoned v2ray-core to meet the needs of users more freely.

## Features

- [x] Implement `Real Direct` traffic split (need ipforward on) to achieve [high performance](https://docs.google.com/spreadsheets/d/1UaWU6nNho7edBNjNqC8dfGXLlW0-cm84MM7sH6Gp7UE/edit?usp=sharing).
- [x] Support to split traffic by process name in local host.
- [x] Support to split traffic by MAC address in LAN.
- [x] Support to split traffic with invert match rules.
- [x] Support to automatically switch nodes according to policy. That is to say, support to automatically test independent TCP/UDP/IPv4/IPv6 latencies, and then use the best nodes for corresponding traffic according to user-defined policy.
- [x] Support advanced DNS resolution process.
- [x] Support full-cone NAT for shadowsocks, trojan(-go) and socks5 (no test).
- [x] Support various trending proxy protocols, seen in [proxy-protocols.md](./docs/en/proxy-protocols.md).

## Getting Started

Please refer to [Quick Start Guide](./docs/en/README.md) to start using `dae` right away!

## Notes

1. If you setup dae and also a shadowsocks server (or any UDP servers) on the same machine in public network, such as a VPS, don't forget to add `l4proto(udp) && sport(your server ports) -> must_direct` rule for your UDP server port. Because states of UDP are hard to maintain, all outgoing UDP packets will potentially be proxied (depends on your routing), including traffic to your client. This behaviour is not what we want to see. `must_direct` makes all traffic from this port including DNS traffic direct.
1. If users in mainland China find that the first screen time is very long when they visit some domestic websites for the first time, please check whether you use foreign DNS to handle some domestic domain in DNS routing. Sometimes this is hard to spot. For example, `ocsp.digicert.cn` is included in `geosite:geolocation-!cn` unexpectedly, which will cause some tls handshakes to take a long time. Be careful to use such domain sets in DNS routing.
1. `dae run` now includes a built-in console. Use `--console-bind=0.0.0.0:8899` to expose it, or set it to an empty string to disable it. The console reads and writes the config file specified by `--config`, and you can override the config path with `DAE_CONFIG_PATH`. Runtime traffic, DNS, and group snapshots are collected directly from the control plane, not by replaying external logs.

## How it works

See [How it works](./docs/en/how-it-works.md).

## TODO

- [ ] Automatically check dns upstream and source loop (whether upstream is also a client of us) and remind the user to add sip rule.
- [ ] MACv2 extension extraction.
- [ ] Log to userspace.
- [ ] Protocol-oriented node features detecting (or filter), such as full-cone (especially VMess and VLESS).
- [ ] Add quick-start guide
- [ ] ...

## Contributors

Special thanks goes to all [contributors](https://github.com/daeuniverse/dae/graphs/contributors). If you would like to contribute, please see the [instructions](./docs/en/development/contribute.md). Also, it is recommended following the [commit-msg-guide](./docs/en/development/commit-msg-guide.md).

## License

[AGPL-3.0 (C) daeuniverse](https://github.com/daeuniverse/dae/blob/main/LICENSE)

## Stargazers over time

[![Stargazers over time](https://starchart.cc/daeuniverse/dae.svg)](https://starchart.cc/daeuniverse/dae)


## 控制台 Token 与配置读取

### 1. Token 生成与持久化

- 内置控制台 token 由服务端在启动时生成（36 位，字符集为大小写字母与数字）。
- token 文件路径固定为：`dirname(--config)/token.txt`。
- 如果 `token.txt` 已存在且非空，则复用原 token；否则生成新 token 并写入文件（权限 `0600`）。
- 控制台启动时会在日志输出：`[Console] Access token: ...`。

### 2. Token 使用方式

- HTTP API（`/api/*`）要求请求头：`Authorization: Bearer <token>`。
- WebSocket（`/ws`）要求 URL 参数：`?token=<token>`。
- 页面端会将 token 存入浏览器 `localStorage`（key: `dae-console-token`）

### 3. 配置路径判定优先级

控制台读写配置文件与执行 reload/validate 时的路径优先级如下：

1. 环境变量 `DAE_CONFIG_PATH`（非空即优先）
2. `dae run --config <path>` 传入路径
3. 回退默认 `/usr/local/etc/dae/config.dae`

注意：控制台状态栏中的 `config_path` 才是当前后端实际使用路径，排查“页面内容与文件不一致”时应以此为准。

### 4. 与前端编辑状态相关的约束

- 配置编辑器在“有未保存修改”时，不会被自动同步覆盖。
- 因此页面显示内容可能暂时与磁盘文件不一致，这是前端保护行为，不代表后端读取错误。