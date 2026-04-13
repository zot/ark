package ark

// CRC: crc-DB.md | R986

import (
	"log"
	"runtime/debug"
)

// svc sends a fire-and-forget closure to the actor.
// The goroutine wrapper prevents the channel send from blocking the caller.
func svc(ch chan func(), fn func()) {
	go func() { ch <- fn }()
}

// svcSync sends a closure to the actor and blocks until it completes.
func svcSync[T any](ch chan func(), fn func() (T, error)) (T, error) {
	done := make(chan struct{})
	var val T
	var err error
	ch <- func() {
		val, err = fn()
		close(done)
	}
	<-done
	return val, err
}

// svcSyncVoid sends a closure with no return value and blocks until it completes.
func svcSyncVoid(ch chan func(), fn func() error) error {
	done := make(chan struct{})
	var err error
	ch <- func() {
		err = fn()
		close(done)
	}
	<-done
	return err
}

// runSvc starts the actor event loop. Exits when channel is closed.
// Unrecovered panics in closures dump a stack trace and kill the program.
// Closures that expect recoverable panics should handle them internally.
func runSvc(ch chan func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Fatalf("actor panic: %v\n%s", r, debug.Stack())
			}
		}()
		for fn := range ch {
			fn()
		}
	}()
}
