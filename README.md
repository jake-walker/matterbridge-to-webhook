# matterbridge to Webhook

This is a small program to listen on a [matterbridge](https://github.com/42wim/matterbridge) API for messages and forward them to a webhook.

This is being used to build a 'serverless' chat bot, but could also have other uses.

## Usage

### Configuration

The program is configured using the following environment variables:

| Name | Default | Description |
|------|---------|-------------|
| `MATTERBRIDGE_API_URL` | _(none, required)_ | The URL to the base of the matterbridge API (excluding `/api/...`) |
| `MATTERBRIDGE_API_USERNAME` | _(none)_ | The username for basic authentication to the matterbridge API. Defaults to no authentication. |
| `MATTERBRIDGE_API_PASSWORD` | _(none)_ | The password for basic authentication to the matterbridge API. Defaults to no authentication. |
| `WEBHOOK_URL` | _(none, required)_ | The webhook where messages are POSTed to. |
| `MESSAGE_PREFIX` | _(none)_ | Messages without this prefix are ignored. Defaults to accepting all messages. |

### Running

To run, simply configure using the above environment variables, then run the following:

```bash
go run .
```

## Improvements

- [ ] Debounce/throttle inputs so that any messages received in a short time are sent together.
