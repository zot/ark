package ark

// CRC: crc-DB.md | R986

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
func runSvc(ch chan func()) {
	go func() {
		for fn := range ch {
			fn()
		}
	}()
}
