package main

/*
 * An example of how to use incus's golang /dev/incus client. This is intended to
 * be run from inside a container.
 */

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"gopkg.in/yaml.v2"

	"github.com/lxc/incus/v6/shared/api"
)

type devIncusDialer struct {
	Path string
}

func (d devIncusDialer) devIncusDial(ctx context.Context, network, path string) (net.Conn, error) {
	addr, err := net.ResolveUnixAddr("unix", d.Path)
	if err != nil {
		return nil, err
	}

	conn, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		return nil, err
	}

	return conn, err
}

var devIncusTransport = &http.Transport{
	DialContext: devIncusDialer{"/dev/incus/sock"}.devIncusDial,
}

func devIncusMonitorStream() {
	client := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", "/dev/incus/sock")
			},
		},
	}

	resp, err := client.Get("http://unix/1.0/events")
	if err != nil {
		panic(err)
	}

	scanner := bufio.NewScanner(resp.Body)

	for scanner.Scan() {
		message := make(map[string]any)
		err = json.Unmarshal(scanner.Bytes(), &message)
		if err != nil {
			return
		}

		message["timestamp"] = nil

		msg, err := yaml.Marshal(&message)
		if err != nil {
			return
		}

		fmt.Printf("%s\n", msg)
	}
}

func devIncusMonitorWebsocket(c http.Client) {
	dialer := websocket.Dialer{
		NetDialContext:   devIncusTransport.DialContext,
		HandshakeTimeout: time.Second * 5,
	}

	conn, _, err := dialer.Dial("ws://unix.socket/1.0/events", nil)
	if err != nil {
		return
	}

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}

		message := make(map[string]any)
		err = json.Unmarshal(data, &message)
		if err != nil {
			return
		}

		message["timestamp"] = nil

		msg, err := yaml.Marshal(&message)
		if err != nil {
			return
		}

		fmt.Printf("%s\n", msg)
	}
}

func devIncusState(ready bool) {
	client := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", "/dev/incus/sock")
			},
		},
	}

	var body bytes.Buffer
	payload := struct {
		State string `json:"state"`
	}{}

	if ready {
		payload.State = api.Ready.String()
	} else {
		payload.State = api.Started.String()
	}

	err := json.NewEncoder(&body).Encode(&payload)
	if err != nil {
		return
	}

	req, err := http.NewRequest("PATCH", "http://unix/1.0", &body)
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json")

	_, err = client.Do(req)
	if err != nil {
		return
	}
}

func main() {
	c := http.Client{Transport: devIncusTransport}
	raw, err := c.Get("http://meshuggah-rocks/")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if raw.StatusCode != http.StatusOK {
		fmt.Println("http error", raw.StatusCode)
		result, err := io.ReadAll(raw.Body)
		if err != nil {
			os.Exit(1)
		}

		fmt.Println(string(result))
	}

	result := []string{}
	err = json.NewDecoder(raw.Body).Decode(&result)
	if err != nil {
		fmt.Println("err decoding response", err)
		os.Exit(1)
	}

	if result[0] != "/1.0" {
		fmt.Println("unknown response", result)
		os.Exit(1)
	}

	if len(os.Args) > 1 {
		if os.Args[1] == "monitor-websocket" {
			devIncusMonitorWebsocket(c)
			os.Exit(0)
		}

		if os.Args[1] == "monitor-stream" {
			devIncusMonitorStream()
			os.Exit(0)
		}

		if os.Args[1] == "ready-state" {
			ready, err := strconv.ParseBool(os.Args[2])
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}

			devIncusState(ready)
			os.Exit(0)
		}

		var path string
		if os.Args[1] == "devices" {
			path = "devices"
		} else {
			path = fmt.Sprintf("config/%s", os.Args[1])
		}

		raw, err := c.Get(fmt.Sprintf("http://meshuggah-rocks/1.0/%s", path))
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		value, err := io.ReadAll(raw.Body)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		fmt.Println(string(value))
	} else {
		fmt.Println("/dev/incus ok")
	}
}
