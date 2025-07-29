package logging

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lxc/incus/v6/internal/server/state"
	"github.com/lxc/incus/v6/shared/api"
	localtls "github.com/lxc/incus/v6/shared/tls"
)

// This is a modified version of https://github.com/grafana/loki/blob/v1.6.1/pkg/promtail/client/.

const (
	contentType  = "application/json"
	maxErrMsgLen = 1024
)

type config struct {
	batchSize int
	batchWait time.Duration

	caCert   string
	username string
	password string
	labels   []string
	instance string
	location string
	retry    int

	timeout time.Duration
	url     *url.URL
}

type entry struct {
	labels LabelSet
	Entry
}

// LokiLogger represents a Loki client.
type LokiLogger struct {
	common
	cfg     config
	client  *http.Client
	ctx     context.Context
	quit    chan struct{}
	once    sync.Once
	entries chan entry
	wg      sync.WaitGroup
}

// NewLokiLogger returns a logger of loki type.
func NewLokiLogger(s *state.State, name string) (*LokiLogger, error) {
	urlStr, username, password, caCert, instance, labels, retry := s.GlobalConfig.LoggingConfigForLoki(name)

	// Set defaults.
	if retry == 0 {
		retry = 3
	}

	// Validate the URL.
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, err
	}

	// Handle standalone systems.
	var location string
	if !s.ServerClustered {
		hostname, err := os.Hostname()
		if err != nil {
			return nil, err
		}

		location = hostname
		if instance == "" {
			instance = hostname
		}
	} else if instance == "" {
		instance = s.ServerName
	}

	loggerClient := LokiLogger{
		common: newCommonLogger(name, s.GlobalConfig),
		cfg: config{
			batchSize: 10 * 1024,
			batchWait: 1 * time.Second,
			caCert:    caCert,
			username:  username,
			password:  password,
			instance:  instance,
			location:  location,
			labels:    sliceFromString(labels),
			retry:     retry,
			timeout:   10 * time.Second,
			url:       u,
		},
		client:  &http.Client{},
		ctx:     s.ShutdownCtx,
		entries: make(chan entry),
		quit:    make(chan struct{}),
	}

	if caCert != "" {
		tlsConfig, err := localtls.GetTLSConfigMem("", "", caCert, "", false)
		if err != nil {
			return nil, err
		}

		loggerClient.client.Transport = &http.Transport{
			TLSClientConfig: tlsConfig,
		}
	} else {
		loggerClient.client = http.DefaultClient
	}

	return &loggerClient, nil
}

func (l *LokiLogger) run() {
	batch := newBatch()

	minWaitCheckFrequency := 10 * time.Millisecond
	maxWaitCheckFrequency := max(l.cfg.batchWait/10, minWaitCheckFrequency)

	maxWaitCheck := time.NewTicker(maxWaitCheckFrequency)

	defer func() {
		// Send all pending batches
		l.sendBatch(batch)
		l.wg.Done()
	}()

	for {
		select {
		case <-l.ctx.Done():
			return

		case <-l.quit:
			return

		case e := <-l.entries:
			// If adding the entry to the batch will increase the size over the max
			// size allowed, we do send the current batch and then create a new one
			if batch.sizeBytesAfter(e) > l.cfg.batchSize {
				l.sendBatch(batch)

				batch = newBatch(e)
				break
			}

			// The max size of the batch isn't reached, so we can add the entry
			batch.add(e)

		case <-maxWaitCheck.C:
			// Send batch if max wait time has been reached
			if batch.age() < l.cfg.batchWait {
				break
			}

			l.sendBatch(batch)
			batch = newBatch()
		}
	}
}

func (l *LokiLogger) sendBatch(batch *batch) {
	if batch.empty() {
		return
	}

	buf, _, err := batch.encode()
	if err != nil {
		return
	}

	var status int

	for range l.cfg.retry {
		select {
		case <-l.quit:
			return
		default:
			// Try to send the message.
			status, err = l.send(l.ctx, buf)
			if err == nil {
				return
			}

			// Only retry 429s, 500s and connection-level errors.
			if status > 0 && status != 429 && status/100 != 5 {
				return
			}

			// Retry every 10s.
			time.Sleep(10 * time.Second)
		}
	}
}

func (l *LokiLogger) send(ctx context.Context, buf []byte) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, l.cfg.timeout)
	defer cancel()

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/loki/api/v1/push", l.cfg.url.String()), bytes.NewReader(buf))
	if err != nil {
		return -1, err
	}

	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", contentType)

	if l.cfg.username != "" && l.cfg.password != "" {
		req.SetBasicAuth(l.cfg.username, l.cfg.password)
	}

	resp, err := l.client.Do(req)
	if err != nil {
		return -1, err
	}

	if resp.StatusCode/100 != 2 {
		scanner := bufio.NewScanner(io.LimitReader(resp.Body, maxErrMsgLen))
		line := ""

		if scanner.Scan() {
			line = scanner.Text()
		}

		err = fmt.Errorf("server returned HTTP status %s (%d): %s", resp.Status, resp.StatusCode, line)
	}

	return resp.StatusCode, err
}

// Start starts the loki logger.
func (l *LokiLogger) Start() error {
	l.wg.Add(1)
	go l.run()

	return nil
}

// Stop stops the client.
func (l *LokiLogger) Stop() {
	l.once.Do(func() { close(l.quit) })
	l.wg.Wait()
}

// Validate checks whether the logger configuration is correct.
func (l *LokiLogger) Validate() error {
	if l.cfg.url.String() == "" {
		return fmt.Errorf("%s: URL cannot be empty", l.name)
	}

	return nil
}

// HandleEvent handles the event received from the internal event listener.
func (l *LokiLogger) HandleEvent(event api.Event) {
	if !l.processEvent(event) {
		return
	}

	// Support overriding the location field (used on standalone systems).
	location := event.Location
	if l.cfg.location != "" {
		location = l.cfg.location
	}

	entry := entry{
		labels: LabelSet{
			"app":      "incus",
			"type":     event.Type,
			"location": location,
			"instance": l.cfg.instance,
		},
		Entry: Entry{
			Timestamp: event.Timestamp,
		},
	}

	ctx := make(map[string]string)

	switch event.Type {
	case api.EventTypeLifecycle:
		lifecycleEvent := api.EventLifecycle{}

		err := json.Unmarshal(event.Metadata, &lifecycleEvent)
		if err != nil {
			return
		}

		if lifecycleEvent.Name != "" {
			entry.labels["name"] = lifecycleEvent.Name
		}

		if lifecycleEvent.Project != "" {
			entry.labels["project"] = lifecycleEvent.Project
		}

		// Build map. These key-value pairs will either be added as labels, or be part of the
		// log message itself.
		ctx["action"] = lifecycleEvent.Action
		ctx["source"] = lifecycleEvent.Source

		maps.Copy(ctx, buildNestedContext("context", lifecycleEvent.Context))

		if lifecycleEvent.Requestor != nil {
			ctx["requester-address"] = lifecycleEvent.Requestor.Address
			ctx["requester-protocol"] = lifecycleEvent.Requestor.Protocol
			ctx["requester-username"] = lifecycleEvent.Requestor.Username
		}

		// Get a sorted list of context keys.
		keys := make([]string, 0, len(ctx))

		for k := range ctx {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		// Add key-value pairs as labels but don't override any labels.
		for _, k := range keys {
			v := ctx[k]

			if slices.Contains(l.cfg.labels, k) {
				_, ok := entry.labels[k]
				if !ok {
					// Label names may not contain any hyphens.
					entry.labels[strings.ReplaceAll(k, "-", "_")] = v
					delete(ctx, k)
				}
			}
		}

		messagePrefix := ""

		// Add the remaining context as the message prefix.
		for k, v := range ctx {
			messagePrefix += fmt.Sprintf("%s=\"%s\" ", k, v)
		}

		entry.Line = fmt.Sprintf("%s%s", messagePrefix, lifecycleEvent.Action)
	case api.EventTypeLogging, api.EventTypeNetworkACL:
		logEvent := api.EventLogging{}

		err := json.Unmarshal(event.Metadata, &logEvent)
		if err != nil {
			return
		}

		tmpContext := map[string]any{}

		// Convert map[string]string to map[string]any as buildNestedContext takes the latter type.
		for k, v := range logEvent.Context {
			tmpContext[k] = v
		}

		// Build map. These key-value pairs will either be added as labels, or be part of the
		// log message itself.
		ctx["level"] = logEvent.Level

		maps.Copy(ctx, buildNestedContext("context", tmpContext))

		// Add key-value pairs as labels but don't override any labels.
		for k, v := range ctx {
			if slices.Contains(l.cfg.labels, k) {
				_, ok := entry.labels[k]
				if !ok {
					entry.labels[k] = v
					delete(ctx, k)
				}
			}
		}

		keys := make([]string, 0, len(ctx))

		for k := range ctx {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		var message strings.Builder

		// Add the remaining context as the message prefix. The keys are sorted alphabetically.
		for _, k := range keys {
			message.WriteString(fmt.Sprintf("%s=%q ", k, ctx[k]))
		}

		message.WriteString(logEvent.Message)

		entry.Line = message.String()
	}

	l.entries <- entry
}

func buildNestedContext(prefix string, m map[string]any) map[string]string {
	labels := map[string]string{}

	for k, v := range m {
		t := reflect.TypeOf(v)

		if t != nil && t.Kind() == reflect.Map {
			for k, v := range buildNestedContext(k, v.(map[string]any)) {
				if prefix == "" {
					labels[k] = v
				} else {
					labels[fmt.Sprintf("%s-%s", prefix, k)] = v
				}
			}
		} else {
			if prefix == "" {
				labels[k] = fmt.Sprintf("%v", v)
			} else {
				labels[fmt.Sprintf("%s-%s", prefix, k)] = fmt.Sprintf("%v", v)
			}
		}
	}

	return labels
}

// MarshalJSON returns the JSON encoding of Entry.
func (e Entry) MarshalJSON() ([]byte, error) {
	return fmt.Appendf(nil, "[\"%d\", %s]", e.Timestamp.UnixNano(), strconv.Quote(e.Line)), nil
}

// String implements the Stringer interface. It returns a formatted/sorted set of label key/value pairs.
func (l LabelSet) String() string {
	var b strings.Builder

	keys := make([]string, 0, len(l))

	for k := range l {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
			b.WriteByte(' ')
		}

		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(l[k]))
	}

	b.WriteByte('}')
	return b.String()
}
