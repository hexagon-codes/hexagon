package graph

import (
	"fmt"
	"reflect"
	"runtime/debug"
)

type guardedStateCodec[S State] struct {
	codec                   StateCodec[S]
	name, typeName, version string
}

func (c guardedStateCodec[S]) Type() string {
	if c.typeName != "" {
		return c.typeName
	}
	return c.codec.Type()
}
func (c guardedStateCodec[S]) Version() string {
	if c.version != "" {
		return c.version
	}
	return c.codec.Version()
}
func (c guardedStateCodec[S]) Encode(state S) (data []byte, err error) {
	defer func() {
		if value := recover(); value != nil {
			data = nil
			err = &NodePanicError{Node: c.name + " state codec", Value: value, Stack: debug.Stack()}
		}
	}()
	return c.codec.Encode(state)
}
func (c guardedStateCodec[S]) Decode(data []byte) (state S, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = &NodePanicError{Node: c.name + " state codec", Value: value, Stack: debug.Stack()}
		}
	}()
	return c.codec.Decode(data)
}

// sameConcreteTypes 防止接口字段经 JSON 后静默退化为另一种具体类型。
func sameConcreteTypes(before, after reflect.Value) error {
	if !before.IsValid() {
		return nil
	}
	if !after.IsValid() || before.Type() != after.Type() {
		return fmt.Errorf("expected %v, restored %v", before.Type(), valueType(after))
	}
	switch before.Kind() {
	case reflect.Interface, reflect.Pointer:
		if before.IsNil() {
			return nil
		}
		if after.IsNil() {
			return fmt.Errorf("restored nil for %v", before.Type())
		}
		return sameConcreteTypes(before.Elem(), after.Elem())
	case reflect.Struct:
		for i := 0; i < before.NumField(); i++ {
			if before.Type().Field(i).IsExported() {
				if err := sameConcreteTypes(before.Field(i), after.Field(i)); err != nil {
					return err
				}
			}
		}
	case reflect.Map:
		iter := before.MapRange()
		for iter.Next() {
			other := after.MapIndex(iter.Key())
			if !other.IsValid() {
				continue
			}
			if err := sameConcreteTypes(iter.Value(), other); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < before.Len() && i < after.Len(); i++ {
			if err := sameConcreteTypes(before.Index(i), after.Index(i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func valueType(value reflect.Value) any {
	if !value.IsValid() {
		return "missing"
	}
	return value.Type()
}
