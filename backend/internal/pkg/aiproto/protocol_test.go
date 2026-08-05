package aiproto

import (
	"bytes"
	"errors"
	"testing"
)

func TestParseProtocol(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Protocol
		wantErr bool
	}{
		{name: "openai", in: "openai", want: ProtocolOpenAI},
		{name: "anthropic", in: "anthropic", want: ProtocolAnthropic},
		{name: "copilot", in: "copilot", want: ProtocolCopilot},
		{name: "uppercase is accepted", in: "OpenAI", want: ProtocolOpenAI},
		{name: "surrounding space is trimmed", in: "  copilot\t", want: ProtocolCopilot},
		{name: "unknown", in: "gemini", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseProtocol(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrUnknownProtocol) {
					t.Fatalf("ParseProtocol(%q) error = %v, want ErrUnknownProtocol", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseProtocol(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("ParseProtocol(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestProtocolValid(t *testing.T) {
	for _, p := range []Protocol{ProtocolOpenAI, ProtocolAnthropic, ProtocolCopilot} {
		if !p.Valid() {
			t.Errorf("Protocol(%q).Valid() = false, want true", p)
		}
		if p.String() != string(p) {
			t.Errorf("Protocol(%q).String() = %q", p, p.String())
		}
	}
	if Protocol("bedrock").Valid() {
		t.Error(`Protocol("bedrock").Valid() = true, want false`)
	}
}

func TestSSEEventEncode(t *testing.T) {
	tests := []struct {
		name  string
		event SSEEvent
		want  string
	}{
		{
			name:  "openai style has no event line",
			event: SSEEvent{Data: []byte(`{"a":1}`)},
			want:  "data: {\"a\":1}\n\n",
		},
		{
			name:  "anthropic style carries event line",
			event: SSEEvent{Event: "message_stop", Data: []byte(`{"type":"message_stop"}`)},
			want:  "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		},
		{
			name:  "done frame ignores data",
			event: SSEEvent{Data: []byte(`{"ignored":true}`), Done: true},
			want:  "data: [DONE]\n\n",
		},
		{
			name:  "done frame from helper",
			event: DoneEvent(),
			want:  "data: [DONE]\n\n",
		},
		{
			name:  "empty data still terminates the frame",
			event: SSEEvent{},
			want:  "data: \n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(tt.event.Encode()); got != tt.want {
				t.Fatalf("Encode() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSSEEventAppendToReusesBuffer 固化 AppendTo 的语义：它追加而不覆盖，
// 使流式热路径可以复用同一个缓冲区。
func TestSSEEventAppendToReusesBuffer(t *testing.T) {
	buf := make([]byte, 0, 128)
	buf = SSEEvent{Event: "ping", Data: []byte(`{"type":"ping"}`)}.AppendTo(buf)
	buf = SSEEvent{Data: []byte(`{"n":2}`)}.AppendTo(buf)
	buf = DoneEvent().AppendTo(buf)

	want := "event: ping\ndata: {\"type\":\"ping\"}\n\n" +
		"data: {\"n\":2}\n\n" +
		"data: [DONE]\n\n"
	if string(buf) != want {
		t.Fatalf("AppendTo chain = %q, want %q", buf, want)
	}
}

// TestSSEEventEncodeCapacity 确认 Encode 预估的容量足够，不触发二次扩容。
func TestSSEEventEncodeCapacity(t *testing.T) {
	e := SSEEvent{Event: "content_block_delta", Data: bytes.Repeat([]byte("x"), 512)}
	out := e.Encode()
	if cap(out) != len(out) {
		t.Fatalf("Encode() cap = %d, len = %d; capacity estimate should be exact", cap(out), len(out))
	}
}

func TestOptionsNilSafe(t *testing.T) {
	var opt *Options
	opt.warn("must not panic on nil receiver")
	if got := opt.model(); got != "" {
		t.Errorf("nil Options model() = %q, want empty", got)
	}
	if opt.supportPDF() || opt.supportToolContentArray() || opt.supportToolContentImage() {
		t.Error("nil Options support flags should all be false")
	}
	if got := opt.defaultMaxTokens(); got != fallbackMaxTokens {
		t.Errorf("nil Options defaultMaxTokens() = %d, want %d", got, fallbackMaxTokens)
	}
}

func TestOptionsDefaultMaxTokens(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "zero falls back", in: 0, want: fallbackMaxTokens},
		{name: "negative falls back", in: -1, want: fallbackMaxTokens},
		{name: "positive is honored", in: 8192, want: 8192},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := &Options{DefaultMaxTokens: tt.in}
			if got := opt.defaultMaxTokens(); got != tt.want {
				t.Fatalf("defaultMaxTokens() = %d, want %d", got, tt.want)
			}
		})
	}
}

// recordingLogger 收集 warn 调用，用于断言降级事件被上报。
type recordingLogger struct {
	msgs []string
}

func (l *recordingLogger) Warn(msg string, _ ...any) {
	l.msgs = append(l.msgs, msg)
}

func TestOptionsWarnReachesLogger(t *testing.T) {
	lg := &recordingLogger{}
	opt := &Options{Logger: lg}
	opt.warn("dropped field", "field", "top_k")
	if len(lg.msgs) != 1 || lg.msgs[0] != "dropped field" {
		t.Fatalf("logger received %v, want exactly one \"dropped field\"", lg.msgs)
	}
}
