package netutil

import (
	"context"
	"net"
	"time"
)

const (
	waitTimeout       = 10 * time.Second
	waitRetryInterval = 1 * time.Second
)

func WaitForSocket(ctx context.Context, scheme, addr string, timeout time.Duration) bool {
	done := make(chan bool)

	go func() {
		t := time.NewTicker(waitRetryInterval)
		defer t.Stop()

		now := time.Now()

		for {
			d := net.Dialer{Timeout: waitTimeout}
			conn, err := d.DialContext(ctx, scheme, addr)

			if ctx.Err() == context.Canceled {
				done <- false

				close(done)
			}

			if err != nil {
				if time.Since(now) > timeout {
					done <- false

					close(done)

					return
				}

				<-t.C

				continue
			}

			if conn != nil {
				done <- true

				close(done)

				return
			}
		}
	}()

	return <-done
}
