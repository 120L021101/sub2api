package aiproto

import "fmt"

// RequestConverter 把下游协议的请求报文转换为上游协议的请求报文。
//
// 实现必须是无状态的，可被多个 goroutine 并发使用；且不得修改入参 src
// 或其反序列化结果所引用的任何调用方数据。
type RequestConverter interface {
	// From 返回输入报文所属协议。
	From() Protocol
	// To 返回输出报文所属协议。
	To() Protocol
	// ConvertRequest 转换请求报文。src 必须是完整的 JSON 请求体。
	ConvertRequest(src []byte, opt *Options) ([]byte, error)
}

// ResponseConverter 把上游协议的非流式响应报文转换为下游协议的响应报文。
//
// 实现必须是无状态的，可被多个 goroutine 并发使用。
type ResponseConverter interface {
	// From 返回输入报文所属协议。
	From() Protocol
	// To 返回输出报文所属协议。
	To() Protocol
	// ConvertResponse 转换非流式响应报文。
	ConvertResponse(src []byte, opt *Options) ([]byte, error)
}

// StreamConverter 把上游协议的流式响应逐块转换为下游协议的 SSE 事件。
//
// 实现持有流式状态机，非并发安全：必须每个请求创建一个实例。
// 调用方必须在上游流结束（无论正常结束还是出错）后调用 Flush，
// 否则可能丢失末尾的收尾事件，导致下游客户端永久等待。
type StreamConverter interface {
	// From 返回上游报文所属协议。
	From() Protocol
	// To 返回下游事件所属协议。
	To() Protocol
	// ConvertChunk 输入一条上游 SSE 的 data 载荷（不含 "data: " 前缀），
	// 返回 0 到 N 条下游事件。返回空切片是合法且常见的情况。
	ConvertChunk(src []byte) ([]SSEEvent, error)
	// Flush 在上游流结束时调用，返回残留的收尾事件。
	// 允许重复调用，第二次及以后返回空切片。
	Flush() ([]SSEEvent, error)
}

// StreamConverterFactory 构造一个新的流式转换器实例。
type StreamConverterFactory func(opt *Options) StreamConverter

// convKey 是 registry 的查找键。
type convKey struct {
	from Protocol
	to   Protocol
}

// registry。仅在包初始化期间写入，运行期只读，因此无需加锁。
var (
	requestConvs  = make(map[convKey]RequestConverter)
	responseConvs = make(map[convKey]ResponseConverter)
	streamConvs   = make(map[convKey]StreamConverterFactory)
)

// registerRequestConv 注册请求转换器。重复注册同一协议组合会 panic：
// 这是包初始化期的不变量断言，不是运行期错误处理。
func registerRequestConv(c RequestConverter) {
	k := convKey{c.From(), c.To()}
	if _, dup := requestConvs[k]; dup {
		panic(fmt.Sprintf("aiproto: duplicate request converter %s->%s", k.from, k.to))
	}
	requestConvs[k] = c
}

// registerResponseConv 注册响应转换器。重复注册会 panic。
func registerResponseConv(c ResponseConverter) {
	k := convKey{c.From(), c.To()}
	if _, dup := responseConvs[k]; dup {
		panic(fmt.Sprintf("aiproto: duplicate response converter %s->%s", k.from, k.to))
	}
	responseConvs[k] = c
}

// registerStreamConv 注册流式转换器工厂。重复注册会 panic。
func registerStreamConv(from, to Protocol, f StreamConverterFactory) {
	k := convKey{from, to}
	if _, dup := streamConvs[k]; dup {
		panic(fmt.Sprintf("aiproto: duplicate stream converter %s->%s", from, to))
	}
	streamConvs[k] = f
}

// conversionError 构造带协议组合上下文的 ErrUnsupportedConversion。
func conversionError(kind string, from, to Protocol) error {
	return fmt.Errorf("%w: no %s converter for %s->%s", ErrUnsupportedConversion, kind, from, to)
}

// RequestConv 返回 from → to 的请求转换器。
// 协议组合未注册时返回包装了 ErrUnsupportedConversion 的错误。
func RequestConv(from, to Protocol) (RequestConverter, error) {
	c, ok := requestConvs[convKey{from, to}]
	if !ok {
		return nil, conversionError("request", from, to)
	}
	return c, nil
}

// ResponseConv 返回 from → to 的非流式响应转换器。
// 协议组合未注册时返回包装了 ErrUnsupportedConversion 的错误。
func ResponseConv(from, to Protocol) (ResponseConverter, error) {
	c, ok := responseConvs[convKey{from, to}]
	if !ok {
		return nil, conversionError("response", from, to)
	}
	return c, nil
}

// NewStreamConv 构造一个 from → to 的流式转换器实例。
// 返回的实例非并发安全，调用方必须在流结束后调用其 Flush。
// 协议组合未注册时返回包装了 ErrUnsupportedConversion 的错误。
func NewStreamConv(from, to Protocol, opt *Options) (StreamConverter, error) {
	f, ok := streamConvs[convKey{from, to}]
	if !ok {
		return nil, conversionError("stream", from, to)
	}
	return f(opt), nil
}

// 转换器的注册发生在各转换器文件的 init 中；see Task 10 完成接线。
// 在此之前 registry 为空，所有查找都会返回 ErrUnsupportedConversion。
