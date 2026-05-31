// Passwd generates a bcrypt hash of a password read from stdin.
// The hash can be used for the admin_key_hash configuration field.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"pastebin/internal/auth"
)

func main() {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading stdin: %v\n", err)
		os.Exit(1)
	}

	password := strings.TrimSpace(string(input))
	if password == "" {
		fmt.Fprintln(os.Stderr, "error: empty password")
		os.Exit(1)
	}

	hash, err := auth.GenerateHash([]byte(password))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating hash: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(hash)
}
