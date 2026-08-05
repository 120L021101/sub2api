package aiproto

import (
	"errors"
	"strings"
	"testing"
)

// 测试专用的伪协议标识。故意不使用真实协议，避免污染 registry 中的真实组合。
const (
	fakeProtoA Protocol = "test-proto-a"
	fakeProtoB Protocol = "test-proto-b"
)

// fakeConv 同时实现三个转换接口，用于验证 registry 的注册与查找。
type fakeConv struct {
	from Protocol
	to   Protocol
}

func (c fakeConv) From() Protocol { return c.from }
func (c fakeConv) To() Protocol   { return c.to }

func (c fakeConv) ConvertRequest(src []byte, _ *Options) ([]byte, error) {
	return src, nil
}

func (c fakeConv) ConvertResponse(src []byte, _ *Options) ([]byte, error) {
	return src, nil
}

func (c fakeConv) ConvertChunk([]byte) ([]SSEEvent, error) { return nil, nil }

func (c fakeConv) Flush() ([]SSEEvent, error) { return nil, nil }

// TestRegistryUnsupported 固化未注册组合的行为：三类查找都必须返回
// 包装了 ErrUnsupportedConversion 的错误，而不是 nil 转换器或 panic。
func TestRegistryUnsupported(t *testing.T) {
	const missing Protocol = "test-proto-missing"

	if _, err := RequestConv(missing, ProtocolCopilot); !errors.Is(err, ErrUnsupportedConversion) {
		t.Errorf("RequestConv error = %v, want ErrUnsupportedConversion", err)
	}
	if _, err := ResponseConv(missing, ProtocolCopilot); !errors.Is(err, ErrUnsupportedConversion) {
		t.Errorf("ResponseConv error = %v, want ErrUnsupportedConversion", err)
	}
	sc, err := NewStreamConv(missing, ProtocolCopilot, nil)
	if !errors.Is(err, ErrUnsupportedConversion) {
		t.Errorf("NewStreamConv error = %v, want ErrUnsupportedConversion", err)
	}
	if sc != nil {
		t.Error("NewStreamConv should return a nil converter alongside the error")
	}
}

// TestRegistryErrorMentionsProtocols 确认错误信息带协议组合上下文，
// 否则排查「为什么没走转换」时无从下手。
func TestRegistryErrorMentionsProtocols(t *testing.T) {
	_, err := RequestConv(ProtocolAnthropic, Protocol("nope"))
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"anthropic", "nope", "request"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}

// registerForTest 把 c 注册进三个 registry，并在测试结束时移除。
//
// 必须清理：registry 是包级全局状态，测试注册后不清理会让 go test -count=2
// 在第二轮撞上重复注册 panic，整个测试二进制崩溃。
func registerForTest(t *testing.T, c fakeConv) {
	t.Helper()
	registerRequestConv(c)
	registerResponseConv(c)
	registerStreamConv(c.from, c.to, func(*Options) StreamConverter { return c })
	t.Cleanup(func() {
		k := convKey{c.from, c.to}
		delete(requestConvs, k)
		delete(responseConvs, k)
		delete(streamConvs, k)
	})
}

func TestRegisterAndLookup(t *testing.T) {
	registerForTest(t, fakeConv{from: fakeProtoA, to: fakeProtoB})

	rc, err := RequestConv(fakeProtoA, fakeProtoB)
	if err != nil {
		t.Fatalf("RequestConv error: %v", err)
	}
	if rc.From() != fakeProtoA || rc.To() != fakeProtoB {
		t.Fatalf("RequestConv returned %s->%s", rc.From(), rc.To())
	}

	if _, err := ResponseConv(fakeProtoA, fakeProtoB); err != nil {
		t.Fatalf("ResponseConv error: %v", err)
	}

	stream, err := NewStreamConv(fakeProtoA, fakeProtoB, nil)
	if err != nil {
		t.Fatalf("NewStreamConv error: %v", err)
	}
	if stream == nil {
		t.Fatal("NewStreamConv returned nil converter without error")
	}

	// 反向组合没有注册，必须仍然报不支持。
	if _, err := RequestConv(fakeProtoB, fakeProtoA); !errors.Is(err, ErrUnsupportedConversion) {
		t.Errorf("reverse direction error = %v, want ErrUnsupportedConversion", err)
	}
}

// TestRegisterCleanupRemovesEntries 固化 registerForTest 的清理契约本身，
// 否则 -count>1 崩溃这类问题只会在偶然多跑一轮时才暴露。
func TestRegisterCleanupRemovesEntries(t *testing.T) {
	const (
		tmpA Protocol = "test-cleanup-a"
		tmpB Protocol = "test-cleanup-b"
	)
	t.Run("register", func(t *testing.T) {
		registerForTest(t, fakeConv{from: tmpA, to: tmpB})
		if _, err := RequestConv(tmpA, tmpB); err != nil {
			t.Fatalf("RequestConv error: %v", err)
		}
	})
	// 子测试结束后 t.Cleanup 已执行，registry 必须回到未注册状态。
	if _, err := RequestConv(tmpA, tmpB); !errors.Is(err, ErrUnsupportedConversion) {
		t.Errorf("after cleanup error = %v, want ErrUnsupportedConversion", err)
	}
}

// TestRegisterDuplicatePanics 固化「重复注册是初始化期不变量违规」这一约定：
// 必须立即 panic，而不是静默覆盖导致运行期走到错误的转换器。
func TestRegisterDuplicatePanics(t *testing.T) {
	const (
		dupA Protocol = "test-dup-a"
		dupB Protocol = "test-dup-b"
	)
	c := fakeConv{from: dupA, to: dupB}
	k := convKey{dupA, dupB}

	tests := []struct {
		name     string
		register func()
		cleanup  func()
	}{
		{
			name:     "request",
			register: func() { registerRequestConv(c) },
			cleanup:  func() { delete(requestConvs, k) },
		},
		{
			name:     "response",
			register: func() { registerResponseConv(c) },
			cleanup:  func() { delete(responseConvs, k) },
		},
		{
			name: "stream",
			register: func() {
				registerStreamConv(dupA, dupB, func(*Options) StreamConverter { return c })
			},
			cleanup: func() { delete(streamConvs, k) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.register() // 首次注册成功
			t.Cleanup(tt.cleanup)
			defer func() {
				if recover() == nil {
					t.Error("duplicate registration did not panic")
				}
			}()
			tt.register() // 重复注册必须 panic
		})
	}
}
