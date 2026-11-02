package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/hexagon-codes/hexagon/orchestration/internal/typecompat"
	"github.com/hexagon-codes/toolkit/util/reflectx"
)

type WriteOperation string

const (
	WriteReplace WriteOperation = "replace"
	WriteAppend  WriteOperation = "append"
	WriteDelete  WriteOperation = "delete"
)

// FieldMapping 仅复制声明字段；空路径明确表示整个值。
type FieldMapping struct {
	From      string
	To        string
	Operation WriteOperation
}

// NodeIO 将共享状态与组件输入输出分开，类型信息用于构建和派发前校验。
type NodeIO struct {
	InType  reflect.Type
	OutType reflect.Type
	Input   []FieldMapping
	Output  []FieldMapping
	Invoke  func(context.Context, any) (any, error)
}

type StateWrite struct {
	Path      string
	Operation WriteOperation
	Value     any
}

type FieldReducer func(current, incoming any) (any, error)

// WithFieldReducer 为同字段多来源声明合并规则，调用顺序服从冻结计划。
func (b *GraphBuilder[S]) WithFieldReducer(path string, reducer FieldReducer) *GraphBuilder[S] {
	if b.graph.fieldReducers == nil {
		b.graph.fieldReducers = map[string]FieldReducer{}
	}
	b.graph.fieldReducers[path] = reducer
	return b
}

// NewMappedNode 适配不同输入输出类型，不隐式转换字段或猜测同名映射。
func NewMappedNode[S State, I, O any](name string, handler func(context.Context, I) (O, error), input, output []FieldMapping) *Node[S] {
	return &Node[S]{Name: name, IO: &NodeIO{InType: reflect.TypeFor[I](), OutType: reflect.TypeFor[O](), Input: append([]FieldMapping(nil), input...), Output: append([]FieldMapping(nil), output...), Invoke: func(ctx context.Context, value any) (any, error) {
		typed, ok := value.(I)
		if !ok {
			return nil, fmt.Errorf("node %q input type mismatch: expected %v, got %T", name, reflect.TypeFor[I](), value)
		}
		return handler(ctx, typed)
	}}, Metadata: map[string]any{}}
}

func validateNodeIO[S State](name string, io *NodeIO) error {
	if io.InType == nil || io.OutType == nil || io.Invoke == nil {
		return fmt.Errorf("node %q has incomplete I/O contract", name)
	}
	if len(io.Input) == 0 && !(io.InType.Kind() == reflect.Struct && io.InType.NumField() == 0) {
		return fmt.Errorf("node %q requires explicit input mappings", name)
	}
	for _, mapping := range io.Input {
		from, err := fieldType(reflect.TypeFor[S](), mapping.From)
		if err != nil {
			return fmt.Errorf("node %q input field %q: %w", name, mapping.From, err)
		}
		to, err := fieldType(io.InType, mapping.To)
		if err != nil {
			return fmt.Errorf("node %q input target %q: %w", name, mapping.To, err)
		}
		if typecompat.Check(from, to) == typecompat.Incompatible {
			return fmt.Errorf("node %q cannot map input %q (%v) to %q (%v)", name, mapping.From, from, mapping.To, to)
		}
	}
	seen := map[string]bool{}
	for _, mapping := range io.Output {
		if seen[mapping.To] {
			return fmt.Errorf("node %q has conflicting output mappings for %q", name, mapping.To)
		}
		seen[mapping.To] = true
		to, err := fieldType(reflect.TypeFor[S](), mapping.To)
		if err != nil {
			return fmt.Errorf("node %q output target %q: %w", name, mapping.To, err)
		}
		if mapping.Operation == WriteDelete {
			continue
		}
		from, err := fieldType(io.OutType, mapping.From)
		if err != nil {
			return fmt.Errorf("node %q output field %q: %w", name, mapping.From, err)
		}
		if typecompat.Check(from, to) == typecompat.Incompatible {
			return fmt.Errorf("node %q cannot map output %q (%v) to %q (%v)", name, mapping.From, from, mapping.To, to)
		}
	}
	return nil
}

func fieldType(t reflect.Type, path string) (reflect.Type, error) {
	if path == "" {
		return t, nil
	}
	for _, part := range strings.Split(path, ".") {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		switch t.Kind() {
		case reflect.Interface:
			return t, nil
		case reflect.Map:
			if t.Key().Kind() != reflect.String {
				return nil, fmt.Errorf("field path requires a string-keyed map")
			}
			t = t.Elem()
		case reflect.Struct:
			field, ok := t.FieldByName(part)
			if !ok || !field.IsExported() {
				return nil, fmt.Errorf("field %q is missing or inaccessible in %v", part, t)
			}
			t = field.Type
		default:
			return nil, fmt.Errorf("cannot traverse field %q in %v", part, t)
		}
	}
	return t, nil
}

func readField(value reflect.Value, path string) (reflect.Value, error) {
	if path == "" {
		return value, nil
	}
	for _, part := range strings.Split(path, ".") {
		for value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface {
			if value.IsNil() {
				return reflect.Value{}, fmt.Errorf("nil value before field %q", part)
			}
			value = value.Elem()
		}
		switch value.Kind() {
		case reflect.Struct:
			value = value.FieldByName(part)
		case reflect.Map:
			if value.Type().Key().Kind() != reflect.String {
				return reflect.Value{}, fmt.Errorf("field path requires a string-keyed map")
			}
			key := reflect.ValueOf(part).Convert(value.Type().Key())
			value = value.MapIndex(key)
		default:
			return reflect.Value{}, fmt.Errorf("cannot read field %q in %v", part, value.Type())
		}
		if !value.IsValid() || !value.CanInterface() {
			return reflect.Value{}, fmt.Errorf("field %q is missing or inaccessible", part)
		}
	}
	return value, nil
}

func assignValue(value reflect.Value, target reflect.Type) (reflect.Value, error) {
	for value.IsValid() && value.Kind() == reflect.Interface {
		if value.IsNil() {
			value = reflect.Value{}
			break
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		switch target.Kind() {
		case reflect.Interface, reflect.Pointer, reflect.Map, reflect.Slice:
			return reflect.Zero(target), nil
		}
		return reflect.Value{}, fmt.Errorf("nil is not assignable to %v", target)
	}
	if !value.Type().AssignableTo(target) {
		return reflect.Value{}, fmt.Errorf("value type %v is not assignable to %v", value.Type(), target)
	}
	result := reflect.New(target).Elem()
	result.Set(value)
	return result, nil
}

func writeField(root reflect.Value, path string, value reflect.Value, remove bool) (reflect.Value, error) {
	if path == "" {
		if remove {
			return reflect.Zero(root.Type()), nil
		}
		return assignValue(value, root.Type())
	}
	if root.Kind() == reflect.Interface {
		if root.IsNil() {
			return root, fmt.Errorf("cannot traverse nil interface")
		}
		updated, err := writeField(root.Elem(), path, value, remove)
		if err != nil {
			return root, err
		}
		result := reflect.New(root.Type()).Elem()
		result.Set(updated)
		return result, nil
	}
	if root.Kind() == reflect.Pointer {
		copy := reflect.New(root.Type().Elem())
		if !root.IsNil() {
			copy.Elem().Set(root.Elem())
		}
		updated, err := writeField(copy.Elem(), path, value, remove)
		if err != nil {
			return root, err
		}
		copy.Elem().Set(updated)
		return copy, nil
	}
	parts := strings.SplitN(path, ".", 2)
	name := parts[0]
	rest := ""
	if len(parts) == 2 {
		rest = parts[1]
	}
	switch root.Kind() {
	case reflect.Struct:
		copy := reflect.New(root.Type()).Elem()
		copy.Set(root)
		field := copy.FieldByName(name)
		if !field.IsValid() || !field.CanSet() {
			return root, fmt.Errorf("field %q is missing or inaccessible", name)
		}
		updated, err := writeField(field, rest, value, remove)
		if err != nil {
			return root, err
		}
		field.Set(updated)
		return copy, nil
	case reflect.Map:
		if root.Type().Key().Kind() != reflect.String {
			return root, fmt.Errorf("field path requires a string-keyed map")
		}
		if root.IsNil() {
			root = reflect.MakeMap(root.Type())
		}
		key := reflect.ValueOf(name).Convert(root.Type().Key())
		if remove && rest == "" {
			root.SetMapIndex(key, reflect.Value{})
			return root, nil
		}
		old := root.MapIndex(key)
		if !old.IsValid() {
			old = reflect.Zero(root.Type().Elem())
		}
		updated, err := writeField(old, rest, value, remove)
		if err != nil {
			return root, err
		}
		root.SetMapIndex(key, updated)
		return root, nil
	default:
		return root, fmt.Errorf("cannot write field %q in %v", name, root.Type())
	}
}

func invokeMapped[S State](ctx context.Context, node *Node[S], state S) ([]StateWrite, error) {
	io := node.IO
	input := reflect.New(io.InType).Elem()
	for _, mapping := range io.Input {
		value, err := readField(reflect.ValueOf(state), mapping.From)
		if err != nil {
			return nil, fmt.Errorf("node %q input %q: %w", node.Name, mapping.From, err)
		}
		input, err = writeField(input, mapping.To, value, false)
		if err != nil {
			return nil, fmt.Errorf("node %q input %q: %w", node.Name, mapping.To, err)
		}
	}
	output, err := io.Invoke(ctx, input.Interface())
	if err != nil {
		return nil, err
	}
	value := reflect.ValueOf(output)
	if _, err := assignValue(value, io.OutType); err != nil {
		return nil, fmt.Errorf("node %q output: %w", node.Name, err)
	}
	var writes []StateWrite
	for _, mapping := range io.Output {
		operation := mapping.Operation
		if operation == "" {
			operation = WriteReplace
		}
		var incoming any
		if operation != WriteDelete {
			field, err := readField(value, mapping.From)
			if err != nil {
				return nil, err
			}
			incoming = field.Interface()
		}
		writes = append(writes, StateWrite{mapping.To, operation, incoming})
	}
	return writes, nil
}

func (g *Graph[S]) applyWrites(base S, writes []StateWrite) (output S, err error) {
	defer func() {
		if value := recover(); value != nil {
			output = base
			err = &NodePanicError{Node: "field reducer", Value: value}
		}
	}()
	copy, err := base.Clone()
	if err != nil {
		return base, err
	}
	typed, ok := copy.(S)
	if !ok {
		return base, fmt.Errorf("write state clone has incompatible type %T", copy)
	}
	root := reflect.ValueOf(typed)
	for _, write := range writes {
		incoming, copyErr := reflectx.DeepCopyChecked(write.Value)
		if copyErr != nil {
			return base, copyErr
		}
		value := reflect.ValueOf(incoming)
		if write.Operation != WriteDelete {
			if reducer := g.fieldReducers[write.Path]; reducer != nil {
				old, err := readField(root, write.Path)
				var current any
				if err == nil {
					current = old.Interface()
				}
				next, err := reducer(current, incoming)
				if err != nil {
					return base, err
				}
				value = reflect.ValueOf(next)
			} else if write.Operation == WriteAppend {
				old, err := readField(root, write.Path)
				if err != nil {
					return base, err
				}
				for old.Kind() == reflect.Interface {
					old = old.Elem()
				}
				incoming, err := assignValue(value, old.Type())
				if err != nil || old.Kind() != reflect.Slice {
					return base, fmt.Errorf("append requires matching slice fields at %q", write.Path)
				}
				value = reflect.AppendSlice(old, incoming)
			} else if write.Operation != WriteReplace {
				return base, fmt.Errorf("unknown write operation %q", write.Operation)
			}
		}
		root, err = writeField(root, write.Path, value, write.Operation == WriteDelete)
		if err != nil {
			return base, fmt.Errorf("write field %q: %w", write.Path, err)
		}
	}
	return root.Interface().(S), nil
}

type encodedWrite struct {
	Path      string          `json:"path"`
	Operation WriteOperation  `json:"operation"`
	Value     json.RawMessage `json:"value,omitempty"`
}

func encodeWrites(writes []StateWrite) ([]encodedWrite, error) {
	var result []encodedWrite
	for _, write := range writes {
		data, err := json.Marshal(write.Value)
		if err != nil {
			return nil, err
		}
		result = append(result, encodedWrite{write.Path, write.Operation, data})
	}
	return result, nil
}

func decodeWrites(nodeIO *NodeIO, writes []encodedWrite) ([]StateWrite, error) {
	var result []StateWrite
	for _, write := range writes {
		var t reflect.Type
		for _, mapping := range nodeIO.Output {
			if mapping.To == write.Path {
				var err error
				t, err = fieldType(nodeIO.OutType, mapping.From)
				if err != nil {
					return nil, err
				}
				break
			}
		}
		if t == nil {
			return nil, fmt.Errorf("saved write has no declared mapping: %q", write.Path)
		}
		value := reflect.New(t)
		if err := json.Unmarshal(write.Value, value.Interface()); err != nil {
			return nil, err
		}
		result = append(result, StateWrite{write.Path, write.Operation, value.Elem().Interface()})
	}
	return result, nil
}
