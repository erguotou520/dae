# dae 运维 QA

## 为什么 fast 分组选了延迟更高的节点？

**现象**：HK 02 最新延迟更低，但 fast 分组却选择了 JP 11。

**原因**：fast 分组的策略是 `min_moving_avg`（滑动平均延迟选路），不是按最新一次延迟选路。滑动平均公式为：

```
MovingAverage = (MovingAverage + latency) / 2
```

历史值占 50%，新值占 50%，是指数衰减平均。如果 HK 02 之前有过高延迟或超时记录，即使最近几次延迟很低，滑动平均仍会被历史值拉高。

此外还有**容差机制**（`check_tolerance`，默认 200ms）：只有新节点延迟比当前最优节点低超过 tolerance 时才会切换。所以即使 HK 02 滑动平均略低，差距不到 200ms 也不会切换。

**相关代码**：
- 选路逻辑：`component/outbound/dialer/alive_dialer_set.go` `NotifyLatencyChange()`
- 滑动平均计算：`component/outbound/dialer/connectivity_check.go`

## 为什么分组显示的延迟远高于节点实际延迟？

**现象**：JP 11 最新延迟 230ms，但分组显示 2496ms。

**原因**：分组显示的是 `SortingLatency`（排序延迟），由 `min_moving_avg` 策略下取 `MovingAverage` 值。由于指数衰减平均的特性，如果节点之前有超时记录（默认 8s），恢复过程如下：

```
第1次 timeout 8000ms → avg = 8000ms
第2次 ok    230ms    → avg = (8000 + 230) / 2 = 4115ms
第3次 ok    230ms    → avg = (4115 + 230) / 2 = 2172ms
第4次 ok    230ms    → avg = (2172 + 230) / 2 = 1201ms
...
```

所以 2496ms 说明节点还在从之前的高延迟/超时中恢复，需要多轮健康检查（`check_interval` 默认 300s）才会逐步下降到接近真实延迟。

## 为什么 UDP4 节点全部 offline？

**现象**：控制台分组页面中 UDP4 和 UDP4(DNS) 所有节点状态均为 offline。

**原因**：UDP 健康检查方式是通过代理节点向 `8.8.8.8:53` 发送 DNS 查询（解析 `connectivitycheck.gstatic.com`）来验证 UDP 连通性。大部分代理节点（特别是 vmess/vless/trojan）不保证 UDP 转发可用，或者服务器端 UDP 出口受限，导致 DNS 查询超时，节点被标记为 UDP NOT ALIVE。

代码中 UDP4 和 UDP4(DNS) 共享同一个健康检查结果（`component/outbound/dialer/connectivity_check.go` 中 `mustGetCollection()`）。

**影响**：基本不影响正常使用。大部分实际流量走 TCP（HTTP/HTTPS），DNS 查询走 `dns_upstream` 配置的 TCP 或 UDP，dae 会根据节点能力自动选择。

**缓解方案**：如果需要 UDP 可用，可在配置中指定可靠的 UDP 检查 DNS：

```ini
global {
  udp_check_dns: 'dns.google:53,8.8.8.8'
}
```

但如果代理节点本身不支持 UDP 转发，改检查 DNS 也无法解决。

## pasyun 节点导致整个网络不可用

**现象**：启用 pasyun 节点后，所有走 fast 分组的流量全部失败，网络中断。

**原因**：pasyun 节点配置为 `hysteria2://...@see-seo.gl.at.ply.gg:2922/...`，该域名 DNS 解析经常失败（SERVFAIL）。dae 在启动时需要解析节点域名，如果失败则跳过该节点。但当 pasyun 被选为 fast 分组的活跃 dialer 后，如果 DNS 解析失败导致节点无法创建，所有依赖该 dialer 的流量都会收到 `no alive dialer` 错误。

**修复**：将域名替换为 IP 地址直连，避免 DNS 解析失败：

```ini
node {
  # 原始（不稳定）
  # pasyun: "hysteria2://...@see-seo.gl.at.ply.gg:2922/..."
  # 修复后（IP 直连）
  pasyun: "hysteria2://...@147.185.221.17:2922/..."
}
```

**建议**：对于 DNS 不稳定的节点，优先使用 IP 地址直连。

## 控制台长期打开导致内存泄漏

**现象**：浏览器长时间不关闭控制台页面，内存占用持续增长。

**原因**：
1. WebSocket 每次推送都直接触发 DOM 更新，无节流
2. 日志/流量/DNS 列表无上限，数据不断累积
3. 每次渲染都重建大量 DOM 节点

**修复**：
- 添加 `scheduleRender()` 使用 `requestAnimationFrame` 合并渲染
- 日志上限限制为 1000 条，流量/连接/DNS 限制为 200 条
- 日志渲染只显示最近 200 条，减少 DOM 节点数

## 控制台 IPv6 显示开关不生效

**现象**：勾选"隐藏IPv6"后，tcp6/udp6 分组节点仍然显示。

**原因**：JavaScript 代码在 IIFE（立即执行函数表达式）内部，`onclick="toggleHideIPv6(this.checked)"` 无法访问 IIFE 内部定义的函数。

**修复**：将 `toggleHideIPv6` 挂载到 `window` 对象上：`window.toggleHideIPv6 = toggleHideIPv6`。
