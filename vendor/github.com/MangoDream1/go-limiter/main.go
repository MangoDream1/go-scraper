package limiter

import "sync"

type Limiter interface {
	Add()
	Done()
}

type limiter struct {
	total   int8
	amount  int8
	waiting int8
	done    chan bool
	m       sync.Mutex
}

func (l *limiter) Add() {
	l.m.Lock()

	if l.amount >= l.total {
		l.waiting += 1
		<-l.done
	}

	l.amount += 1
	l.m.Unlock()
}

func (l *limiter) Done() {
	l.amount -= 1

	if l.waiting > 0 {
		l.waiting -= 1
		l.done <- true
	}
}

type noopLimiter struct{}

func (l *noopLimiter) Add()  {}
func (l *noopLimiter) Done() {}

func NewLimiter(total int8) Limiter {
	// -1 is uncapped
	if total == -1 {
		return &noopLimiter{}
	}

	return &limiter{
		total: total,
		m:     sync.Mutex{},
		done:  make(chan bool),
	}
}
