package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/shell"
)

var pushportStdinReader io.Reader = os.Stdin

func newPushPortCmd(appID, baseURL string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pushport",
		Short: "Manage PushPort integration",
	}
	cmd.AddCommand(newPushPortRegisterCmd(appID, baseURL))
	return cmd
}

func registrationURL(base, appID string) string {
	return strings.TrimRight(base, "/") + "/apps/" + appID + "/instances/register"
}

func newPushPortRegisterCmd(appID, baseURL string) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "register",
		Short:         "Register this deployment with PushPort and apply the instance token via the gateway",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if appID == "" {
				return fail(cmd, fmt.Errorf("this argus build has no PushPort app id embedded; rebuild with 'make build PUSHPORT_APP_ID=<your-app-id>'"))
			}

			cfg, err := resolveConfig(cmd)
			if err != nil {
				return fail(cmd, err)
			}
			if cfg.Gateway.URL == "" {
				return fail(cmd, fmt.Errorf("--gateway ws(s)://host is required"))
			}

			url := registrationURL(baseURL, appID)
			shell.StdOutF("Registration URL: %s\n", url)
			if err := openURLFn(url); err != nil {
				shell.StdErrF("Could not open browser automatically. Visit the URL above.\n")
			}

			shell.StdOutF("Paste the instance token (pit_...): ")
			scanner := bufio.NewScanner(pushportStdinReader)
			scanner.Scan()
			if err := scanner.Err(); err != nil {
				return fail(cmd, fmt.Errorf("read token: %w", err))
			}
			token := strings.TrimSpace(scanner.Text())
			if token == "" {
				return fail(cmd, fmt.Errorf("no token provided"))
			}
			if !strings.HasPrefix(token, "pit_") {
				shell.StdErrF("warning: token does not start with pit_ — double-check you copied the right value\n")
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client, err := dialGatewayClient(ctx, cfg.Gateway.URL, cfg.Token)
			if err != nil {
				return fail(cmd, fmt.Errorf("connect: %w", err))
			}
			defer client.Close()

			if err := client.Call(api.MethodPushPortSetToken, api.PushPortSetTokenParams{Token: token}, nil); err != nil {
				return fail(cmd, fmt.Errorf("set token: %w", err))
			}

			shell.StdOutF("Instance token applied to the gateway.\n")
			return nil
		},
	}
	addGatewayClientFlags(cmd.Flags())
	return cmd
}
