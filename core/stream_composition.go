package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	stream "github.com/hexagon-codes/ai-core/streamx"
)

// forwardOwnedStream 将输出关闭或取消传播到所拥有的上游，正常 EOF 不遮蔽错误。
func forwardOwnedStream[T any](ctx context.Context, source *StreamReader[T], release func()) *StreamReader[T] {
	callCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	closeSource := func() {
		once.Do(func() {
			_ = source.Close()
			if release != nil {
				release()
			}
		})
	}
	stop := context.AfterFunc(callCtx, closeSource)
	reader, writer := stream.PipeWithClose[T](10, func() error { cancel(); closeSource(); return nil })
	go func() {
		defer writer.Close()
		defer closeSource()
		defer cancel()
		defer stop()
		defer func() {
			if value := recover(); value != nil {
				_ = writer.CloseWithError(fmt.Errorf("stream source panicked: %v", value))
			}
		}()
		for {
			value, err := source.Recv()
			if cause := callCtx.Err(); cause != nil {
				_ = writer.CloseWithError(cause)
				return
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					_ = writer.CloseWithError(err)
				}
				return
			}
			if err := writer.Send(value); err != nil {
				return
			}
		}
	}()
	return reader
}

func composeTransform[I, M, O any](ctx context.Context, input *StreamReader[I], first Runnable[I, M], second Runnable[M, O], opts ...Option) (*StreamReader[O], error) {
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(callCtx, func() { _ = input.Close() })
	mid, err := first.Transform(callCtx, input, opts...)
	if err != nil {
		stop()
		cancel()
		_ = input.Close()
		return nil, err
	}
	stopMid := context.AfterFunc(callCtx, func() { _ = mid.Close() })
	out, err := second.Transform(callCtx, mid, opts...)
	if err != nil {
		stopMid()
		stop()
		cancel()
		_ = mid.Close()
		_ = input.Close()
		return nil, err
	}
	return forwardOwnedStream(callCtx, out, func() { stopMid(); stop(); cancel(); _ = mid.Close(); _ = input.Close() }), nil
}

func collectThenInvoke[I, O any](ctx context.Context, input *StreamReader[I], invoke func(context.Context, I) (O, error)) *StreamReader[O] {
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(callCtx, func() { _ = input.Close() })
	reader, writer := stream.PipeWithClose[O](1, func() error { cancel(); return input.Close() })
	go func() {
		defer writer.Close()
		defer input.Close()
		defer cancel()
		defer stop()
		defer func() {
			if value := recover(); value != nil {
				_ = writer.CloseWithError(fmt.Errorf("stream transform panicked: %v", value))
			}
		}()
		value, err := stream.Concat(callCtx, input)
		if err == nil {
			var out O
			out, err = invoke(callCtx, value)
			if err == nil {
				err = writer.Send(out)
			}
		}
		if err != nil {
			_ = writer.CloseWithError(err)
		}
	}()
	return reader
}

// MapEach 显式将输入流的每个元素视为一次独立调用。
// 默认 Transform 会先收集完整输入；只有此适配器才逐元素调用 Invoke。
func MapEach[I, O any](r Runnable[I, O]) Runnable[I, O] {
	br := NewRunnable[I, O](r.Name()+".map_each", r.Description(), r.Invoke)
	br.WithStream(r.Stream)
	br.WithTransform(func(ctx context.Context, input *StreamReader[I], opts ...Option) (*StreamReader[O], error) {
		callCtx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(callCtx, func() { _ = input.Close() })
		reader, writer := stream.PipeWithClose[O](10, func() error { cancel(); return input.Close() })
		go func() {
			defer writer.Close()
			defer input.Close()
			defer cancel()
			defer stop()
			defer func() {
				if value := recover(); value != nil {
					_ = writer.CloseWithError(fmt.Errorf("stream map panicked: %v", value))
				}
			}()
			for {
				value, err := input.Recv()
				if cause := callCtx.Err(); cause != nil {
					_ = writer.CloseWithError(cause)
					return
				}
				if err != nil {
					if !errors.Is(err, io.EOF) {
						_ = writer.CloseWithError(err)
					}
					return
				}
				out, err := r.Invoke(callCtx, value, opts...)
				if err != nil {
					_ = writer.CloseWithError(err)
					return
				}
				if err := writer.Send(out); err != nil {
					return
				}
			}
		}()
		return reader, nil
	})
	br.WithCollect(func(ctx context.Context, input *StreamReader[I], opts ...Option) (O, error) {
		out, err := br.Transform(ctx, input, opts...)
		if err != nil {
			var zero O
			return zero, err
		}
		return stream.Concat(ctx, out)
	})
	return br
}
