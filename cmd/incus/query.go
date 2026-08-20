package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lxc/incus/v7/cmd/incus/color"
	u "github.com/lxc/incus/v7/cmd/incus/usage"
	"github.com/lxc/incus/v7/internal/i18n"
	"github.com/lxc/incus/v7/shared/api"
	cli "github.com/lxc/incus/v7/shared/cmd"
	"github.com/lxc/incus/v7/shared/logger"
)

type cmdQuery struct {
	global *cmdGlobal

	flagRespWait bool
	flagRespRaw  bool
	flagAction   string
	flagData     string
	flagDataFile string
}

var cmdQueryUsage = u.Usage{u.Placeholder(i18n.G("API path")).Remote()}

func (c *cmdQuery) command() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.Use = cli.U("query", cmdQueryUsage...)
	cmd.Short = i18n.G("Send a raw query to the server")
	cmd.Long = cli.FormatSection(color.DescriptionPrefix, i18n.G(
		`Send a raw query to the server`,
	))
	cmd.Example = cli.FormatSection("", i18n.G(
		`incus query -X DELETE --wait /1.0/instances/c1
    Delete local instance "c1".`,
	))
	cmd.Hidden = true

	cmd.RunE = c.run
	cli.AddBoolFlag(cmd.Flags(), &c.flagRespWait, "wait", i18n.G("Wait for the operation to complete"))
	cli.AddBoolFlag(cmd.Flags(), &c.flagRespRaw, "raw", i18n.G("Print the raw response"))
	cli.AddStringFlag(cmd.Flags(), &c.flagAction, "request|X", "GET", "", i18n.G("Action"))
	cli.AddStringFlag(cmd.Flags(), &c.flagData, "data|d", "", "", i18n.G("Input data"))
	cli.AddStringFlag(cmd.Flags(), &c.flagDataFile, "data-file", "", "", i18n.G("Input data file (\"-\" for stdin)"))

	return cmd
}

func (c *cmdQuery) pretty(input any) string {
	pretty := bytes.NewBufferString("")
	enc := json.NewEncoder(pretty)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "\t")
	err := enc.Encode(input)
	if err != nil {
		return fmt.Sprintf("%v", input)
	}

	return pretty.String()
}

func (c *cmdQuery) run(cmd *cobra.Command, args []string) error {
	parsed, err := c.global.Parse(cmdQueryUsage, cmd, args)
	if err != nil {
		return err
	}

	d := parsed[0].RemoteServer
	path := parsed[0].RemoteObject.String

	if c.global.flagProject != "" {
		return errors.New(i18n.G("--project cannot be used with the query command"))
	}

	if c.flagData != "" && c.flagDataFile != "" {
		return errors.New(i18n.G("--data cannot be used with --data-file"))
	}

	if !slices.Contains([]string{"GET", "PUT", "POST", "PATCH", "DELETE"}, c.flagAction) {
		return fmt.Errorf(i18n.G("Action %q isn't supported by this tool"), c.flagAction)
	}

	// Validate path
	if !strings.HasPrefix(path, "/") {
		return errors.New(i18n.G("Query path must start with /"))
	}

	// Guess the encoding of the input
	var data any
	err = json.Unmarshal([]byte(c.flagData), &data)
	if err != nil {
		data = c.flagData
	}

	var input *os.File
	if c.flagDataFile != "" {
		if isStdin(c.flagDataFile) {
			input = os.Stdin
		} else {
			input, err = os.Open(c.flagDataFile)
			if err != nil {
				return fmt.Errorf(i18n.G("Failed to open input file %q: %w"), c.flagDataFile, err)
			}

			defer logger.WarnOnErrorExcept(input.Close, []error{os.ErrClosed}, "Failed to close file")
		}

		data = input
	}

	// Perform the query
	resp, _, err := d.RawQuery(c.flagAction, path, data, "")
	if err != nil {
		var jsonSyntaxError *json.SyntaxError
		var jsonUnmarshalTypeError *json.UnmarshalTypeError

		// If not JSON decoding error then fail immediately.
		if !errors.As(err, &jsonSyntaxError) && !errors.As(err, &jsonUnmarshalTypeError) && err.Error() != "EOF" {
			if c.flagRespRaw && resp != nil {
				fmt.Println(c.pretty(resp))
				return nil
			}

			return err
		}

		// If JSON decoding error then try a plain request.
		cleanErr := err

		// Get the URL prefix
		httpInfo, err := d.GetConnectionInfo()
		if err != nil {
			return err
		}

		// Setup input.
		var rs io.Reader
		if input != nil {
			// Rewind the input so that it can be sent again.
			_, err := input.Seek(0, io.SeekStart)
			if err != nil {
				return cleanErr
			}

			rs = input
		} else if c.flagData != "" {
			rs = bytes.NewReader([]byte(c.flagData))
		}

		// Setup the request
		req, err := http.NewRequest(c.flagAction, fmt.Sprintf("%s%s", httpInfo.URL, path), rs)
		if err != nil {
			return err
		}

		// Set the encoding accordingly
		req.Header.Set("Content-Type", "plain/text")

		resp, err := d.DoHTTP(req)
		if err != nil {
			return err
		}

		if resp.StatusCode != http.StatusOK {
			return cleanErr
		}

		content, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		fmt.Print(string(content))

		return nil
	}

	if c.flagRespWait && resp.Operation != "" {
		uri, err := url.ParseRequestURI(resp.Operation)
		if err != nil {
			return err
		}

		resp, _, err = d.RawQuery("GET", fmt.Sprintf("%s/wait?%s", uri.Path, uri.RawQuery), "", "")
		if err != nil {
			return err
		}

		op := api.Operation{}
		err = json.Unmarshal(resp.Metadata, &op)
		if err == nil && op.Err != "" {
			return errors.New(op.Err)
		}
	}

	if c.flagRespRaw {
		fmt.Println(c.pretty(resp))
	} else if resp.Metadata != nil && string(resp.Metadata) != "{}" {
		var content any
		err := json.Unmarshal(resp.Metadata, &content)
		if err != nil {
			return err
		}

		if content != nil {
			fmt.Println(c.pretty(content))
		}
	}

	return nil
}
