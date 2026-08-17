package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"sb-ui/internal/auth"
)

// runAuthCommand handles the credential subcommands. They are CLI-only on purpose: the API
// must not be able to change the password that protects it.
func runAuthCommand(cmd string) {
	switch cmd {
	case "--new-token":
		// Printed, not stored: the token lives in the unit's EnvironmentFile, which is the
		// operator's to edit. Storing it here too would just be a second copy to leak.
		fmt.Printf("%s=%s\n", auth.TokenEnv, auth.NewToken())
		fmt.Fprintln(os.Stderr, "\nAdd that line to /opt/saltbox-ui/.env and restart the service.")
	case "--set-password":
		if err := setPasswordInteractive(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println("Password set. Sessions already signed in stay valid — use --rotate-sessions to end them.")
	}
}

func setPasswordInteractive() error {
	pw, err := readSecret("New password: ")
	if err != nil {
		return err
	}
	again, err := readSecret("Repeat password: ")
	if err != nil {
		return err
	}
	if pw != again {
		return fmt.Errorf("passwords do not match")
	}
	return auth.SetPassword(pw)
}

// stdin is buffered once and shared: a fresh bufio.Reader per call would lose whatever the
// previous one had already pulled into its buffer, so the second prompt hit EOF when both
// lines arrived together through a pipe.
var stdin = bufio.NewReader(os.Stdin)

// readSecret prompts without echoing. When stdin is not a terminal (a pipe, or ansible) it
// falls back to reading a line, so the command is still scriptable.
func readSecret(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
