package dependencies

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/sourcegraph/sourcegraph/dev/sg/internal/check"
	"github.com/sourcegraph/sourcegraph/dev/sg/internal/std"
	"github.com/sourcegraph/sourcegraph/dev/sg/internal/usershell"
	"github.com/sourcegraph/sourcegraph/dev/sg/root"
	"github.com/sourcegraph/sourcegraph/lib/errors"
)

func aptGetInstall(pkg string, preinstall ...string) check.FixAction[CheckArgs] {
	commands := []string{
		`sudo apt-get update`,
	}
	commands = append(commands, preinstall...)
	commands = append(commands, fmt.Sprintf("sudo apt-get install -y %s", pkg))
	return cmdFixes(commands...)
}

// Ubuntu declares Ubuntu dependencies.
var Ubuntu = []category{
	{
		Name: depsBaseUtilities,
		Checks: []*dependency{
			{
				Name:  "gcc",
				Check: checkAction(check.InPath("gcc")),
				Fix:   aptGetInstall("build-essential"),
			},
			{
				Name:  "git",
				Check: checkAction(check.Combine(check.InPath("git"), checkGitVersion(">= 3.34.1"))),
				Fix:   aptGetInstall("git", "sudo add-apt-repository ppa:git-core/ppa"),
			}, {
				Name:  "pcre",
				Check: checkAction(check.HasUbuntuLibrary("libpcre3-dev")),
				Fix:   aptGetInstall("libpcre3-dev"),
			},
			{
				Name:  "sqlite",
				Check: checkAction(check.HasUbuntuLibrary("libsqlite3-dev")),
				Fix:   aptGetInstall("libsqlite3-dev"),
			},
			{
				Name:  "libev",
				Check: checkAction(check.HasUbuntuLibrary("libev-dev")),
				Fix:   aptGetInstall("libev-dev"),
			},
			{
				Name:  "pkg-config",
				Check: checkAction(check.InPath("pkg-config")),
				Fix:   aptGetInstall("pkg-config"),
			},
			{
				Name:  "jq",
				Check: checkAction(check.InPath("jq")),
				Fix:   aptGetInstall("jq"),
			},
			{
				Name:  "curl",
				Check: checkAction(check.InPath("curl")),
				Fix:   aptGetInstall("curl"),
			},
			// Comby will fail systematically on linux/arm64 as there aren't binaries available for that platform.
			{
				Name:  "comby",
				Check: checkAction(check.InPath("comby")),
				Fix:   cmdFix(`bash <(curl -sL get-comby.netlify.app)`),
			},
			{
				Name:  "bash",
				Check: checkAction(check.CommandOutputContains("bash --version", "version 5")),
				Fix:   aptGetInstall("bash"),
			},
			{
				Name: "asdf",
				// TODO add the if Keegan check
				Check: checkAction(check.CommandOutputContains("asdf", "version")),
				Fix: func(ctx context.Context, cio check.IO, args CheckArgs) error {
					if err := usershell.Run(ctx, "git clone https://github.com/asdf-vm/asdf.git ~/.asdf --branch v0.9.0").StreamLines(cio.Verbose); err != nil {
						return err
					}
					return usershell.Run(ctx,
						`echo ". $HOME/.asdf/asdf.sh" >>`, usershell.ShellConfigPath(ctx),
					).Wait()
				},
			},
		},
	},
	{
		Name:    depsDocker,
		Enabled: disableInCI(), // Very wonky in CI
		Checks: []*dependency{
			{
				Name:  "Docker",
				Check: checkAction(check.InPath("docker")),
				Fix: aptGetInstall(
					"docker-ce docker-ce-cli",
					"curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo apt-key add -",
					fmt.Sprintf(`sudo add-apt-repository "deb [arch=%s] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable`, runtime.GOARCH)),
			},
			{
				Name: "Docker without sudo",
				Check: checkAction(check.Combine(
					check.InPath("docker"),
					// It's possible that the user that installed Docker this way needs sudo to run it, which is not
					// convenient. The following check diagnose that case.
					check.CommandOutputContains("docker ps", "CONTAINER")),
				),
				Fix: func(ctx context.Context, cio check.IO, args CheckArgs) error {
					if err := usershell.Command(ctx, "sudo groupadd docker || true").Run().StreamLines(cio.Verbose); err != nil {
						return err
					}
					if err := usershell.Command(ctx, "sudo usermod -aG docker $USER").Run().StreamLines(cio.Verbose); err != nil {
						return err
					}
					err := check.CommandOutputContains("docker ps", "CONTAINER")(ctx)
					if err != nil {
						cio.WriteAlertf(`You may need to restart your terminal for the permissions needed for Docker to take effect or you can run "newgrp docker" and restart the processe in this terminal.`)
					}
					return err
				},
			},
		},
	},
	categoryCloneRepositories(),
	{
		Name:      "Programming languages & tooling",
		DependsOn: []string{depsCloneRepo, depsBaseUtilities},
		Enabled:   enableOnlyInSourcegraphRepo(),
		Checks: []*check.Check[CheckArgs]{
			{
				Name:  "go",
				Check: checkGoVersion,
				Fix: func(ctx context.Context, cio check.IO, args CheckArgs) error {
					if err := forceASDFPluginAdd(ctx, "golang", "https://github.com/kennyp/asdf-golang.git"); err != nil {
						return err
					}
					return root.Run(usershell.Command(ctx, "asdf install golang")).StreamLines(cio.Verbose)
				},
			},
			{
				Name:  "yarn",
				Check: checkYarnVersion,
				Fix: func(ctx context.Context, cio check.IO, args CheckArgs) error {
					if err := forceASDFPluginAdd(ctx, "yarn", ""); err != nil {
						return err
					}
					return root.Run(usershell.Command(ctx, "asdf install yarn")).StreamLines(cio.Verbose)
				},
			},
			{
				Name:  "node",
				Check: checkNodeVersion,
				Fix: func(ctx context.Context, cio check.IO, args CheckArgs) error {
					if err := forceASDFPluginAdd(ctx, "nodejs", "https://github.com/asdf-vm/asdf-nodejs.git"); err != nil {
						return err
					}
					return cmdFixes(
						`grep -s "legacy_version_file = yes" ~/.asdfrc >/dev/null || echo 'legacy_version_file = yes' >> ~/.asdfrc`,
						"asdf install nodejs",
					)(ctx, cio, args)
				},
			},
			{
				Name:  "rust",
				Check: checkRustVersion,
				Fix: func(ctx context.Context, cio check.IO, args CheckArgs) error {
					if err := forceASDFPluginAdd(ctx, "rust", "https://github.com/asdf-community/asdf-rust.git"); err != nil {
						return err
					}
					return root.Run(usershell.Command(ctx, "asdf install rust")).StreamLines(cio.Verbose)
				},
			},
		},
	},
	{
		Name:      "Postgres database",
		DependsOn: []string{depsBaseUtilities},
		Checks: []*dependency{
			{
				Name:  "Install Postgres",
				Check: checkAction(check.Combine(check.InPath("psql"), check.InPath("pg_ctl"))),
				Fix:   aptGetInstall("postgresql postgresql-contrib"),
			},
			{
				Name: "Start Postgres",
				// In the eventuality of the user using a non standard configuration and having
				// set it up appropriately in its configuration, we can bypass the standard postgres
				// check and directly check for the sourcegraph database.
				//
				// Because only the latest error is returned, it's better to finish with the real check
				// for error message clarity.
				Check: func(ctx context.Context, out *std.Output, args CheckArgs) error {
					if err := checkSourcegraphDatabase(ctx, out, args); err == nil {
						return nil
					}
					return checkPostgresConnection(ctx)
				},
				Description: `Sourcegraph requires the PostgreSQL database to be running.

We recommend installing it with Homebrew and starting it as a system service.
If you know what you're doing, you can also install PostgreSQL another way.
For example: you can use https://postgresapp.com/

If you're not sure: use the recommended commands to install PostgreSQL.`,
				Fix: func(ctx context.Context, cio check.IO, args CheckArgs) error {
					if err := usershell.Command(ctx, "sudo systemctl enable --now postgresql").Run().StreamLines(cio.Verbose); err != nil {
						return err
					}
					if err := usershell.Command(ctx, "sudo -u postgres createuser --superuser $USER").Run().StreamLines(cio.Verbose); err != nil {
						return err
					}

					// Wait for startup
					time.Sleep(5 * time.Second)

					// Doesn't matter if this succeeds
					_ = usershell.Cmd(ctx, "createdb").Run()
					return nil
				},
			},
			{
				Name:        "Connection to 'sourcegraph' database",
				Check:       checkSourcegraphDatabase,
				Description: `Once PostgreSQL is installed and running, we need to set up Sourcegraph database itself and a specific user.`,
				Fix: cmdFixes(
					"createuser --superuser sourcegraph || true",
					`psql -c "ALTER USER sourcegraph WITH PASSWORD 'sourcegraph';"`,
					`createdb --owner=sourcegraph --encoding=UTF8 --template=template0 sourcegraph`,
				),
			},
		},
	},
	{
		Name:      "Redis database",
		DependsOn: []string{depsHomebrew},
		Checks: []*dependency{
			{
				Name:  "Install Redis",
				Check: checkAction(check.InPath("redic-cli")),
				Fix:   aptGetInstall("redis-server"),
			},
			{
				Name: "Start Redis",
				Description: `Sourcegraph requires the Redis database to be running.
We recommend installing it with Homebrew and starting it as a system service.`,
				Check: checkAction(check.Retry(checkRedisConnection, 5, 500*time.Millisecond)),
				Fix:   cmdFix("sudo systemctl enable --now redis-server.service"),
			},
		},
	},
	{
		Name:      "sourcegraph.test development proxy",
		DependsOn: []string{depsBaseUtilities},
		Checks: []*dependency{
			{
				Name: "/etc/hosts contains sourcegraph.test",
				Description: `Sourcegraph should be reachable under https://sourcegraph.test:3443.
To do that, we need to add sourcegraph.test to the /etc/hosts file.`,
				Check: checkAction(check.FileContains("/etc/hosts", "sourcegraph.test")),
				Fix: func(ctx context.Context, cio check.IO, args CheckArgs) error {
					return root.Run(usershell.Command(ctx, `./dev/add_https_domain_to_hosts.sh`)).StreamLines(cio.Verbose)
				},
			},
			{
				Name: "Caddy root certificate is trusted by system",
				Description: `In order to use TLS to access your local Sourcegraph instance, you need to
trust the certificate created by Caddy, the proxy we use locally.

YOU NEED TO RESTART 'sg setup' AFTER RUNNING THIS COMMAND!`,
				Enabled: disableInCI(), // Can't seem to get this working
				Check:   checkAction(checkCaddyTrusted),
				Fix: func(ctx context.Context, cio check.IO, args CheckArgs) error {
					return root.Run(usershell.Command(ctx, `./dev/caddy.sh trust`)).StreamLines(cio.Verbose)
				},
			},
		},
	},
	categoryAdditionalSGConfiguration(),
	{
		Name:      "Cloud services",
		DependsOn: []string{depsBaseUtilities},
		Enabled:   enableForTeammatesOnly(),
		Checks: []*dependency{
			dependencyGcloud(),
			{
				Name:  "1password",
				Check: checkAction(check1password()),
				Fix: func(ctx context.Context, cio check.IO, args CheckArgs) error {
					if err := usershell.Run(ctx, "curl -sS https://downloads.1password.com/linux/keys/1password.asc | sudo gpg --dearmor --output /usr/share/keyrings/1password-archive-keyring.gpg").StreamLines(cio.Verbose); err != nil {
						return err
					}
					if err := usershell.Run(ctx, `echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/1password-archive-keyring.gpg] https://downloads.1password.com/linux/debian/$(dpkg --print-architecture) stable main" |  sudo tee /etc/apt/sources.list.d/1password.list`).StreamLines(cio.Verbose); err != nil {
						return err
					}
					if err := usershell.Run(ctx, `sudo mkdir -p /etc/debsig/policies/AC2D62742012EA22/`).StreamLines(cio.Verbose); err != nil {
						return err
					}
					if err := usershell.Run(ctx, `curl -sS https://downloads.1password.com/linux/debian/debsig/1password.pol | sudo tee /etc/debsig/policies/AC2D62742012EA22/1password.pol`).StreamLines(cio.Verbose); err != nil {
						return err
					}
					if err := usershell.Run(ctx, `sudo mkdir -p /usr/share/debsig/keyrings/AC2D62742012EA22`).StreamLines(cio.Verbose); err != nil {
						return err
					}
					if err := usershell.Run(ctx, `curl -ss https://downloads.1password.com/linux/keys/1password.asc | sudo gpg --dearmor --output /usr/share/debsig/keyrings/ac2d62742012ea22/debsig.gpg`).StreamLines(cio.Verbose); err != nil {
						return err
					}
					if err := usershell.Run(ctx, `sudo apt update && sudo apt install 1password-cli`).StreamLines(cio.Verbose); err != nil {
						return err
					}
					// phew

					if cio.Input == nil {
						return errors.New("interactive input required")
					}

					cio.Write("Enter secret key:")
					var key string
					if _, err := fmt.Fscan(cio.Input, &key); err != nil {
						return err
					}
					cio.Write("Enter account email:")
					var email string
					if _, err := fmt.Fscan(cio.Input, &email); err != nil {
						return err
					}
					cio.Write("Enter account password:")
					var password string
					if _, err := fmt.Fscan(cio.Input, &password); err != nil {
						return err
					}

					return usershell.Command(ctx,
						"op account add --signin --address team-sourcegraph.1password.com --email", email).
						Env(map[string]string{"OP_SECRET_KEY": key}).
						Input(strings.NewReader(password)).
						Run().
						StreamLines(cio.Verbose)
				},
			},
		},
	},
}
