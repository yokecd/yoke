package main

import "sync"

type queue[T any] struct {
	mu   sync.Mutex
	data []T
}

func (queue *queue[T]) push(value T) {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.data = append(queue.data, value)
}

func (queue *queue[T]) pop() (value T, ok bool) {
	queue.mu.Lock()
	defer queue.mu.Unlock()

	if len(queue.data) == 0 {
		return
	}

	value = queue.data[0]
	queue.data = queue.data[1:]
	return
}

type Queue[T comparable] struct {
	barrier *sync.Map
	buffer  *queue[T]
	pipe    chan T
}

func (queue *Queue[T]) Push(value T) {
	if _, loaded := queue.barrier.LoadOrStore(queue, struct{}{}); loaded {
		return
	}

	select {
	case queue.pipe <- value:
	default:
		queue.buffer.push(value)
	}
}

func (queue *Queue[T]) Pop() (value T) {
	defer func() {
		queue.barrier.Delete(value)
	}()

	value, ok := queue.buffer.pop()
	if ok {
		return value
	}

	return <-queue.pipe
}

func (queue *Queue[T]) C() chan T {
	result := make(chan T)

	go func() {
		for {
			result <- queue.Pop()
		}
	}()

	return result
}

func QueueFromChannel[T comparable](c chan T) *Queue[T] {
	queue := Queue[T]{
		barrier: &sync.Map{},
		buffer: &queue[T]{
			mu:   sync.Mutex{},
			data: []T{},
		},
		pipe: make(chan T),
	}

	go func() {
		for value := range c {
			queue.Push(value)
		}
	}()

	return &queue
}
