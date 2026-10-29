package logger

import (
	"fmt"
)

type Prefixed struct {
	prefix string
}

func NewPrefixed(prefix string) *Prefixed {
	return &Prefixed{prefix: prefix}
}

func (p *Prefixed) withPrefix(format string) string {
	return fmt.Sprintf("[%s] %s", p.prefix, format)
}

func (p *Prefixed) Debug(format string, args ...any) {
	Debug(p.withPrefix(format), args...)
}

func (p *Prefixed) Info(format string, args ...any) {
	Info(p.withPrefix(format), args...)
}

func (p *Prefixed) Warning(format string, args ...any) {
	Warning(p.withPrefix(format), args...)
}

func (p *Prefixed) Error(err error, format string, args ...any) {
	Error(err, p.withPrefix(format), args...)
}
