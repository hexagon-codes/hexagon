---
name: Bug Report
about: 报告一个 Bug 帮助我们改进
title: '[Bug] '
labels: 'bug'
assignees: ''
---

## Bug 描述

请清晰简洁地描述 Bug 是什么。

## 复现步骤

1. 导入 '...'
2. 调用 '...'
3. 使用参数 '...'
4. 看到错误

## 期望行为

请清晰简洁地描述你期望发生什么。

## 实际行为

请清晰简洁地描述实际发生了什么。

## 代码示例

```go
// 最小复现代码
package main

import (
    "github.com/hexagon-codes/hexagon"
)

func main() {
    // ...
}
```

## 错误信息

```
// 粘贴完整的错误信息
```

## 环境信息

- OS: [例如 macOS 14.0, Ubuntu 22.04]
- `go version` 输出:
- `go env GOOS GOARCH GOTOOLCHAIN GOWORK` 输出:
- `go list -m github.com/hexagon-codes/hexagon` 输出（使用未发布代码时请提供 commit SHA）:

## 额外信息

添加任何其他有助于理解问题的上下文。

## 检查清单

- [ ] 我已搜索过现有 Issues，确认这是一个新问题
- [ ] 我已提供最小复现代码
- [ ] 我已提供完整的错误信息
