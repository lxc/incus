# generate-config

A small CLI to parse comments in a Golang codebase meant to be used for a documentation tool (like Sphinx for example).

It parses the comments from the AST and extracts their documentation.

# Usage

    $ generate-config -h
    Usage of generate-config:
    -e value
        Path that will be excluded from the process

# Formatting

A comment is formatted this way:

```go
	// gendoc:generate(entity=cluster, group=cluster, key=scheduler.instance)
	//
	//  <Possibly a very long documentation on multiple lines with Markdown tables, etc.>
	// ---
	//  shortdesc: Possible values are all, manual and group. See Automatic placement of instances for more information.
    	//  condition: container
	//  defaultdesc: `all`
	//  type: integer
	//  liveupdate: `yes`
	//  <ANY_KEY>: <ANY_VALUE>
    clusterConfigKeys := map[string]func(value string) error{
		"scheduler.instance": validate.Optional(validate.IsOneOf("all", "group", "manual")),
	}

    for k, v := range config {
		// gendoc:generate(entity=cluster, group=cluster, key=user.*)
		//
		// This is the real long desc.
		//
		// With two paragraphs.
		//
		// And a list:
		//
		// - Item
		// - Item
		// - Item
		//
		// example of a table:
		//
		// Key                                 | Type      | Scope     | Default                                          | Description
		// :--                                 | :---      | :----     | :------                                          | :----------
		// `acme.agree_tos`                    | bool      | global    | `false`                                          | Agree to ACME terms of service
		// `acme.ca_url`                       | string    | global    | `https://acme-v02.api.letsencrypt.org/directory` | URL to the directory resource of the ACME service
		// `acme.domain`                       | string    | global    | -                                                | Domain for which the certificate is issued
		// `acme.email`                        | string    | global    | -                                                | Email address used for the account registration
		//
		//  ---
		//	shortdesc: Free form user key/value storage (can be used in search).
		//	condition: container
		//	default: -
		//	type: string
		//	liveupdate: `yes`
		if strings.HasPrefix(k, "user.") {
			continue
		}

		validator, ok := clusterConfigKeys[k]
		if !ok {
			return fmt.Errorf("Invalid cluster configuration key %q", k)
		}

		err := validator(v)
		if err != nil {
			return fmt.Errorf("Invalid cluster configuration key %q value", k)
		}
	}

	return nil
```

The go-swagger spec from source generator can only handles `swagger:meta` (global file/package level documentation), `swagger:route` (API endpoints), `swagger:params` (function parameters), `swagger:operation` (method documentation), `swagger:response` (API response content documentation), `swagger:model` (struct documentation) generation. In our use case, we would want a config variable spec generator that can bundle any key-value data pairs alongside metadata to build a sense of hierarchy and identity (we want to associate a unique key to each gendoc comment group that will also be displayed in the generated documentation)

In a swagger fashion, `generate-config` can associate metadata key-value pairs (here for example, `group` and `key`) to data key-value pairs. As a result, it can generate a YAML tree out of the code documentation and also a Markdown document.

## Output

Here is the JSON output of the example shown above:

```json
{
	"configs": {
		"cluster": [
			{
				"scheduler.instance": {
					"condition": "container",
					"defaultdesc": "`all`",
					"liveupdate": "`yes`",
					"longdesc": "<Possibly a very long documentation on multiple lines with Markdown tables, etc.>",
					"shortdesc": " Possible values are all, manual and group. See Automatic placement of instances for more",
					"type": "integer"
				}
			},
			{
				"user.*": {
					"condition": "container",
					"defaultdesc": "-",
					"liveupdate": "`yes`",
					"longdesc": "
						This is the real long desc.

						With two paragraphs.

						And a list:

						- Item
						- Item
						- Item

						And a table:

						Key                                 | Type      | Scope     | Default                                          | Description
						:--                                 | :---      | :----     | :------                                          | :----------
						`acme.agree_tos`                    | bool      | global    | `false`                                          | Agree to ACME terms of service
						`acme.ca_url`                       | string    | global    | `https://acme-v02.api.letsencrypt.org/directory` | URL to the directory resource of the ACME service
						`acme.domain`                       | string    | global    | -                                                | Domain for which the certificate is issued
						`acme.email`                        | string    | global    | -                                                | Email address used for the account registration
					",
					"shortdesc": "Free form user key/value storage (can be used in search).",
					"type": "string"
				}
			}
		],
	}
}
```

Here is the `.txt` output of the example shown above:

```
<!-- config group cluster start -->
\`\`\`{config:option} user.* cluster
:type: string
:liveupdate: `yes`
:shortdesc: Free form user key/value storage (can be used in search).
:condition: container
:default: -

This is the real long desc.

With two paragraphs.

And a list:

- Item
- Item
- Item

example of a table:

Key                                 | Type      | Scope     | Default                                          | Description
:--                                 | :---      | :----     | :------                                          | :----------
`acme.agree_tos`                    | bool      | global    | `false`                                          | Agree to ACME terms of service
`acme.ca_url`                       | string    | global    | `https://acme-v02.api.letsencrypt.org/directory` | URL to the directory resource of the ACME service
`acme.domain`                       | string    | global    | -                                                | Domain for which the certificate is issued
`acme.email`                        | string    | global    | -                                                | Email address used for the account registration


\`\`\`

\`\`\`{config:option} scheduler.instance cluster
:liveupdate: `yes`
:shortdesc: Possible values are all, manual and group. See Automatic placement of instances for more information.
:condition: container
:default: `all`
:type: integer


\`\`\`

<!-- config group cluster end -->
```


