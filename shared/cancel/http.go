package cancel

import (
	"context"
	"fmt"
	"net/http"
	"sync"
)

// HTTPRequestCanceller tracks a cancelable operation.
type HTTPRequestCanceller struct {
	reqCancel map[*http.Request]context.CancelFunc
	lock      sync.Mutex
}

// NewHTTPRequestCanceller returns a new HTTPRequestCanceller struct.
func NewHTTPRequestCanceller() *HTTPRequestCanceller {
	c := HTTPRequestCanceller{}

	c.lock.Lock()
	c.reqCancel = make(map[*http.Request]context.CancelFunc)
	c.lock.Unlock()

	return &c
}

// Cancelable indicates whether there are operations that support cancellation.
func (c *HTTPRequestCanceller) Cancelable() bool {
	c.lock.Lock()
	length := len(c.reqCancel)
	c.lock.Unlock()

	return length > 0
}

// Cancel will attempt to cancel all ongoing operations.
func (c *HTTPRequestCanceller) Cancel() error {
	if !c.Cancelable() {
		return fmt.Errorf("This operation can't be canceled at this time")
	}

	c.lock.Lock()
	for req, cancel := range c.reqCancel {
		cancel()
		delete(c.reqCancel, req)
	}

	c.lock.Unlock()

	return nil
}

// CancelableDownload performs an http request and allows for it to be canceled at any time.
func CancelableDownload(c *HTTPRequestCanceller, do func(req *http.Request) (*http.Response, error), req *http.Request) (*http.Response, chan bool, error) {
	chDone := make(chan bool)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	if c != nil {
		c.lock.Lock()
		c.reqCancel[req] = cancel
		c.lock.Unlock()
	}

	go func() {
		<-chDone
		if c != nil {
			c.lock.Lock()
			cancel()
			delete(c.reqCancel, req)
			c.lock.Unlock()
		}
	}()

	resp, err := do(req)
	if err != nil {
		close(chDone)
		return nil, nil, err
	}

	return resp, chDone, nil
}
