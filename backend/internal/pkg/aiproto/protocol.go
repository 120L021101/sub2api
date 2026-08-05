package aiproto

import (
	"errors"
	"strings"
)

// 协议标识。用于 registry 查找与错误信息。
const (
	// ProtocolOpenAI 表示 OpenAI Chat Completions 协议。
	ProtocolOpenAI Protocol = "openai"
	// ProtocolAnthropic 表示 Anthropic Messages 协议。
	ProtocolAnthropic Protocol = "anthropic"
	// ProtocolCopilot 表示 GitHub Copilot 私有方言（OpenAI Chat Completions 的超集）。
	ProtocolCopilot Protocol = "copilot"
)

// Protocol 是本包支持的协议标识。
type Protocol string

// String 实现 fmt.Stringer。
func (p Protocol) String() string { return string(p) }

// Valid 报告 p 是否为本包已知的协议。
func (p Protocol) Valid() bool {
	switch p {
	case ProtocolOpenAI, ProtocolAnthropic, ProtocolCopilot:
		return true
	default:
		return false
	}
}

// ParseProtocol 解析协议名，大小写不敏感。未知协议返回 ErrUnknownProtocol。
func ParseProtocol(s string) (Protocol, error) {
	p := Protocol(strings.ToLower(strings.TrimSpace(s)))
	if !p.Valid() {
		return "", ErrUnknownProtocol
	}
	return p, nil
}

// 本包的 sentinel error。调用方应使用 errors.Is 判定。
var (
	// ErrUnknownProtocol 表示协议名无法识别。
	ErrUnknownProtocol = errors.New("aiproto: unknown protocol")
	// ErrUnsupportedConversion 表示请求的 (from, to) 协议组合没有对应的转换器。
	ErrUnsupportedConversion = errors.New("aiproto: unsupported conversion")
	// ErrMalformedPayload 表示输入报文无法按声明的协议解析。
	ErrMalformedPayload = errors.New("aiproto: malformed payload")
)

// Logger 是本包需要的最小日志抽象，*slog.Logger 天然满足该接口。
//
// 只暴露 Warn：本包的日志用途仅限于上报「协议转换过程中发生了信息丢失或降级」，
// 这类事件既不是错误（转换仍然成功）也不该在正常路径打点。
type Logger interface {
	Warn(msg string, args ...any)
}

// Options 携带转换过程需要的外部上下文。所有字段均可为零值，
// 此时按各字段注释描述的保守默认行为处理。
//
// Options 可以为 nil，本包的所有方法都对 nil 接收器安全。
type Options struct {
	// Model 是本次请求的模型 ID（客户端视角的名字）。
	// 影响 Anthropic → OpenAI 转换中与 Claude 相关的 thinking 过滤规则。
	Model string

	// Logger 接收降级与丢弃事件。为 nil 时静默。
	Logger Logger

	// SupportPDF 表示上游是否接受 file content part（PDF/文档）。
	// 为 false 时 Anthropic 的 document block 会降级为文本提示。
	SupportPDF bool

	// SupportToolContentArray 表示上游是否接受 tool 消息的 content 为数组。
	// 为 false 时纯文本的 tool_result 会被合并为单个字符串。
	SupportToolContentArray bool

	// SupportToolContentImage 表示上游是否接受 tool 消息 content 中的图片。
	// 为 false 时富内容会被搬迁到一条独立的 user 消息中。
	SupportToolContentImage bool

	// StripModelDateSuffix 控制模型名归一化时是否剥离日期后缀，
	// 例如 claude-sonnet-4-5-20250929 是否归一为 claude-sonnet-4.5。
	StripModelDateSuffix bool

	// MessageIDPrefix 非空时，OpenAI → Anthropic 转换会用它替换响应 id 的前缀
	// （Copilot 回显的是 chatcmpl-xxx，部分 Anthropic 客户端要求 msg_ 前缀）。
	MessageIDPrefix string

	// DefaultMaxTokens 用于 OpenAI → Anthropic 转换：Anthropic 的 max_tokens 必填，
	// 而 OpenAI 侧选填。为 0 时使用 fallbackMaxTokens。
	DefaultMaxTokens int

	// InjectCopilotCacheControl 控制 OpenAI → Copilot 请求转换是否注入
	// copilot_cache_control 标记。
	InjectCopilotCacheControl bool
}

// warn 上报一次降级/丢弃事件。Options 或 Logger 为 nil 时静默。
func (o *Options) warn(msg string, args ...any) {
	if o == nil || o.Logger == nil {
		return
	}
	o.Logger.Warn(msg, args...)
}

// model 返回模型 ID，Options 为 nil 时返回空串。
func (o *Options) model() string {
	if o == nil {
		return ""
	}
	return o.Model
}

// supportPDF 返回上游是否接受 file content part。
func (o *Options) supportPDF() bool {
	return o != nil && o.SupportPDF
}

// supportToolContentArray 返回上游是否接受 tool 消息 content 为数组。
func (o *Options) supportToolContentArray() bool {
	return o != nil && o.SupportToolContentArray
}

// supportToolContentImage 返回上游是否接受 tool 消息 content 中的图片。
func (o *Options) supportToolContentImage() bool {
	return o != nil && o.SupportToolContentImage
}

// fallbackMaxTokens 是 Anthropic max_tokens 的兜底值。
// 取值偏保守：宁可截断也不要因超出模型上限而被上游拒绝。
const fallbackMaxTokens = 4096

// defaultMaxTokens 返回 OpenAI → Anthropic 转换使用的 max_tokens 默认值。
func (o *Options) defaultMaxTokens() int {
	if o == nil || o.DefaultMaxTokens <= 0 {
		return fallbackMaxTokens
	}
	return o.DefaultMaxTokens
}

// SSE 线格式的固定片段。
const (
	sseEventPrefix = "event: "
	sseDataPrefix  = "data: "
	sseDoneData    = "[DONE]"
)

// SSEEvent 是一条待写往下游的 SSE 事件。
//
// Anthropic 的流式协议要求每条事件带 event: 行，OpenAI 的流式协议不带；
// 因此 Event 为空表示不写 event: 行。
type SSEEvent struct {
	// Event 是 SSE 的事件名。为空表示不写 event: 行。
	Event string
	// Data 是已序列化的 JSON 报文。Done 为 true 时忽略该字段。
	Data []byte
	// Done 为 true 表示这是 data: [DONE] 终止帧。
	Done bool
}

// AppendTo 把 e 按 SSE 线格式追加到 dst 并返回结果。
//
// 提供 append 形式而非只提供 Encode，是为了让流式热路径可以复用同一个缓冲区，
// 避免每条事件一次分配。
func (e SSEEvent) AppendTo(dst []byte) []byte {
	if e.Event != "" {
		dst = append(dst, sseEventPrefix...)
		dst = append(dst, e.Event...)
		dst = append(dst, '\n')
	}
	dst = append(dst, sseDataPrefix...)
	if e.Done {
		dst = append(dst, sseDoneData...)
	} else {
		dst = append(dst, e.Data...)
	}
	return append(dst, '\n', '\n')
}

// Encode 返回 e 的 SSE 线格式字节。
func (e SSEEvent) Encode() []byte {
	// 预估容量：event 行 + data 行 + 两个换行。
	n := len(sseDataPrefix) + len(e.Data) + 2
	if e.Event != "" {
		n += len(sseEventPrefix) + len(e.Event) + 1
	}
	if e.Done {
		n += len(sseDoneData)
	}
	return e.AppendTo(make([]byte, 0, n))
}

// DoneEvent 返回 OpenAI 风格的终止帧。
func DoneEvent() SSEEvent { return SSEEvent{Done: true} }
