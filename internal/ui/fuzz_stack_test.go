package ui

import "runtime"

func runtimeStack(buf []byte) int { return runtime.Stack(buf, false) }
