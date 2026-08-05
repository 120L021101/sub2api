// Package aiproto 提供 GitHub Copilot、OpenAI Chat Completions、Anthropic Messages
// 三种协议的数据模型与相互转换能力。
//
// # 中枢模型
//
// 本包以 OpenAI Chat Completions 作为中枢中间表示（IR），Copilot 方言被建模为
// 它的超集：
//
//	Anthropic Messages
//	      ↕  真翻译（有损，语义鸿沟见下）
//	OpenAI Chat Completions  ← 中枢 / IR
//	      ↕  字段级 patch（近无损）
//	Copilot 方言 = CC + 私有扩展
//
// 这样建模的依据是：Copilot 的 /chat/completions 请求体与 OpenAI 兼容，差异集中
// 在少量私有字段（reasoning_text、reasoning_opaque、thinking_budget、
// copilot_cache_control、copilot_usage）、端点 path 与请求头。
//
// Copilot 私有字段被保留在 copilot.go 的类型上，使得 Anthropic 的 thinking 与
// signature 可以直达 Copilot 的 reasoning_text 与 reasoning_opaque，不会因为
// 「经过纯净 OpenAI」而丢失。
//
// # 并发约定
//
// RequestConverter 与 ResponseConverter 的实现是无状态的，可被多个 goroutine
// 并发使用。StreamConverter 持有流式状态机，非并发安全，必须每个请求创建一个
// 实例，并在流结束时调用 Flush。
//
// # 边界
//
// 本包只做协议数据转换，不负责 HTTP 出网、token 交换、账号调度与限流。
package aiproto
