package interrupt

import (
	"encoding/json"
	"fmt"
	"reflect"
	"runtime/debug"
	"sync"
)

// EncodedState 保存组件状态的持久类型、版本与内容。
type EncodedState struct {
	Type    string          `json:"type"`
	Version string          `json:"version"`
	Data    json.RawMessage `json:"data"`
}

// CodecPanicError 将组件状态 codec 的 panic 保留为不可盲重放的恢复失败。
type CodecPanicError struct {
	Value any
	Stack []byte
}

func (e *CodecPanicError) Error() string {
	return fmt.Sprintf("interrupt: state codec panicked: %v", e.Value)
}
func (*CodecPanicError) UnsafeToReplay() bool { return true }

type stateCodec struct {
	name    string
	version string
	typeOf  reflect.Type
	encode  func(any) ([]byte, error)
	decode  func([]byte) (any, error)
}

// StateCodecs 由应用装配并持有；恢复只使用已注册的具体版本。
type StateCodecs struct {
	mu        sync.RWMutex
	byType    map[reflect.Type]stateCodec
	byVersion map[string]stateCodec
}

func NewStateCodecs() *StateCodecs {
	return &StateCodecs{byType: map[reflect.Type]stateCodec{}, byVersion: map[string]stateCodec{}}
}

// RegisterStateCodec 显式绑定具体 Go 类型与可持久化格式，不执行业务 IO。
func RegisterStateCodec[T any](registry *StateCodecs, name, version string, encode func(T) ([]byte, error), decode func([]byte) (T, error)) error {
	if registry == nil || name == "" || version == "" || encode == nil || decode == nil {
		return fmt.Errorf("interrupt: invalid state codec")
	}
	c := stateCodec{name: name, version: version, typeOf: reflect.TypeFor[T](), encode: func(v any) ([]byte, error) {
		typed, ok := v.(T)
		if !ok {
			return nil, fmt.Errorf("interrupt: state type mismatch for %s", name)
		}
		return encode(typed)
	}, decode: func(data []byte) (any, error) { return decode(data) }}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := name + "\x00" + version
	if old, ok := registry.byVersion[key]; ok {
		if old.typeOf == c.typeOf {
			return nil
		}
		return fmt.Errorf("interrupt: state codec already registered: %s/%s", name, version)
	}
	if _, ok := registry.byType[c.typeOf]; ok {
		return fmt.Errorf("interrupt: state type already registered: %v", c.typeOf)
	}
	registry.byType[c.typeOf] = c
	registry.byVersion[key] = c
	return nil
}

// RegisterJSONState 注册已具有 JSON 合同的组件类型。
func RegisterJSONState[T any](registry *StateCodecs, name, version string) error {
	return RegisterStateCodec(registry, name, version, func(v T) ([]byte, error) { return json.Marshal(v) }, func(data []byte) (T, error) { var value T; err := json.Unmarshal(data, &value); return value, err })
}

func (c *StateCodecs) Encode(state any) (encoded *EncodedState, err error) {
	defer func() {
		if value := recover(); value != nil {
			encoded = nil
			err = &CodecPanicError{Value: value, Stack: debug.Stack()}
		}
	}()
	if state == nil {
		return nil, nil
	}
	if c == nil {
		return nil, fmt.Errorf("interrupt: no codec registered for state %T", state)
	}
	c.mu.RLock()
	codec, ok := c.byType[reflect.TypeOf(state)]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("interrupt: no codec registered for state %T", state)
	}
	data, err := codec.encode(state)
	if err != nil {
		return nil, fmt.Errorf("interrupt: encode %s/%s: %w", codec.name, codec.version, err)
	}
	return &EncodedState{Type: codec.name, Version: codec.version, Data: data}, nil
}

func (c *StateCodecs) Decode(state *EncodedState) (decoded any, err error) {
	defer func() {
		if value := recover(); value != nil {
			decoded = nil
			err = &CodecPanicError{Value: value, Stack: debug.Stack()}
		}
	}()
	if state == nil {
		return nil, nil
	}
	if c == nil {
		return nil, fmt.Errorf("interrupt: state codec is unavailable: %s/%s", state.Type, state.Version)
	}
	c.mu.RLock()
	codec, ok := c.byVersion[state.Type+"\x00"+state.Version]
	c.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("interrupt: state codec is unavailable: %s/%s", state.Type, state.Version)
	}
	value, err := codec.decode(append([]byte(nil), state.Data...))
	if err != nil {
		return nil, fmt.Errorf("interrupt: decode %s/%s: %w", state.Type, state.Version, err)
	}
	if reflect.TypeOf(value) != codec.typeOf {
		return nil, fmt.Errorf("interrupt: decoded state type mismatch for %s/%s", state.Type, state.Version)
	}
	return value, nil
}

// PersistedSignal 复用原中断身份和 Address；组件状态先解码再注入恢复上下文。
type PersistedSignal struct {
	ID      string             `json:"id"`
	Address Address            `json:"address"`
	Info    json.RawMessage    `json:"info,omitempty"`
	State   *EncodedState      `json:"state,omitempty"`
	Subs    []*PersistedSignal `json:"subs,omitempty"`
	IsRoot  bool               `json:"is_root"`
}

func EncodeSignal(signal *InterruptSignal, codecs *StateCodecs) (encoded *PersistedSignal, err error) {
	defer func() {
		if value := recover(); value != nil {
			encoded = nil
			err = &CodecPanicError{Value: value, Stack: debug.Stack()}
		}
	}()
	if signal == nil {
		return nil, nil
	}
	state, err := codecs.Encode(signal.State)
	if err != nil {
		return nil, err
	}
	info, err := json.Marshal(signal.Info)
	if err != nil {
		return nil, err
	}
	result := &PersistedSignal{ID: signal.ID, Address: append(Address(nil), signal.Address...), Info: info, State: state, IsRoot: signal.IsRoot}
	for _, child := range signal.Subs {
		encoded, err := EncodeSignal(child, codecs)
		if err != nil {
			return nil, err
		}
		result.Subs = append(result.Subs, encoded)
	}
	return result, nil
}

func DecodeSignal(saved *PersistedSignal, codecs *StateCodecs) (*InterruptSignal, error) {
	if saved == nil {
		return nil, nil
	}
	state, err := codecs.Decode(saved.State)
	if err != nil {
		return nil, err
	}
	var info any
	if len(saved.Info) > 0 {
		if err := json.Unmarshal(saved.Info, &info); err != nil {
			return nil, fmt.Errorf("interrupt: decode signal info: %w", err)
		}
	}
	result := &InterruptSignal{ID: saved.ID, Address: append(Address(nil), saved.Address...), Info: info, State: state, IsRoot: saved.IsRoot}
	for _, child := range saved.Subs {
		decoded, err := DecodeSignal(child, codecs)
		if err != nil {
			return nil, err
		}
		result.Subs = append(result.Subs, decoded)
	}
	return result, nil
}
