package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"time"
)

// A broker is a fresh controller created by Windows outside the caller's job.
// It reads credentials itself; none are passed through the broker protocol.
type lifecycleBrokerContextKey struct{}

type lifecycleBrokerReply struct {
	Token  string `json:"token"`
	Code   int    `json:"code"`
	Output []byte `json:"output"`
}

func brokerableLifecycleOperation(target, operation string) bool {
	switch target {
	case "app":
		return operation == "start" || operation == "restart"
	case "daemon":
		return operation == "start" || operation == "restart" || operation == "install" || operation == "update"
	default:
		return false
	}
}

func maybeBrokerLifecycle(ctx context.Context, target, operation, root, state string, confirm bool, output io.Writer) (bool, int) {
	if ctx.Value(lifecycleBrokerContextKey{}) != nil || !brokerableLifecycleOperation(target, operation) {
		return false, 0
	}
	inJob, err := lifecycleControllerInJob()
	if err != nil {
		return true, cliFailure(output, fmt.Errorf("inspect controller job: %w", err))
	}
	if !inJob {
		return false, 0
	}
	code, body, err := invokeLifecycleBroker(ctx, target, operation, root, state, confirm)
	if err != nil {
		return true, cliFailure(output, err)
	}
	if _, err := output.Write(body); err != nil {
		return true, 1
	}
	return true, code
}

func runLifecycleBroker(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("lifecycle-broker", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	target := flags.String("target", "", "lifecycle target")
	operation := flags.String("action", "", "lifecycle action")
	root := flags.String("root", "", "installation root")
	state := flags.String("state-dir", "", "durable state")
	reply := flags.String("reply", "", "local reply address")
	token := flags.String("token", "", "reply token")
	confirm := flags.Bool("confirm-active", false, "confirmed active sessions")
	if err := flags.Parse(args); err != nil {
		return cliFailure(output, err)
	}
	host, port, splitErr := net.SplitHostPort(*reply)
	portNumber, portErr := strconv.Atoi(port)
	tokenBytes, tokenErr := hex.DecodeString(*token)
	if flags.NArg() != 0 || !brokerableLifecycleOperation(*target, *operation) ||
		!filepath.IsAbs(*root) || !filepath.IsAbs(*state) ||
		splitErr != nil || host != "127.0.0.1" || portErr != nil || portNumber < 1 || portNumber > 65535 ||
		tokenErr != nil || len(tokenBytes) != 32 {
		return cliFailure(output, errors.New("invalid independent lifecycle controller arguments"))
	}

	var body bytes.Buffer
	code := 1
	inJob, err := lifecycleControllerInJob()
	if err != nil {
		code = cliFailure(&body, fmt.Errorf("inspect independent controller job: %w", err))
	} else if inJob {
		code = cliFailure(&body, errors.New("independent lifecycle controller is still inside a Windows job"))
	} else {
		ctx := context.WithValue(context.Background(), lifecycleBrokerContextKey{}, true)
		lifecycleArgs := []string{*operation, "--root", *root, "--state-dir", *state}
		if *target == "daemon" && *confirm {
			lifecycleArgs = append(lifecycleArgs, "--confirm-active")
		}
		if *target == "app" {
			code = runAppContext(ctx, lifecycleArgs, &body)
		} else {
			code = runDaemonContext(ctx, lifecycleArgs, &body)
		}
	}

	connection, err := net.DialTimeout("tcp4", *reply, 5*time.Second)
	if err != nil {
		return cliFailure(output, fmt.Errorf("send independent controller result: %w", err))
	}
	defer connection.Close()
	_ = connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(connection).Encode(lifecycleBrokerReply{Token: *token, Code: code, Output: body.Bytes()}); err != nil {
		return cliFailure(output, fmt.Errorf("send independent controller result: %w", err))
	}
	return code
}

func waitLifecycleBrokerReply(ctx context.Context, listener *net.TCPListener, token string) (int, []byte, error) {
	defer listener.Close()
	for {
		if err := ctx.Err(); err != nil {
			return 1, nil, fmt.Errorf("independent lifecycle result not confirmed: %w", err)
		}
		_ = listener.SetDeadline(time.Now().Add(250 * time.Millisecond))
		connection, err := listener.Accept()
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				continue
			}
			return 1, nil, fmt.Errorf("receive independent lifecycle result: %w", err)
		}
		_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
		var reply lifecycleBrokerReply
		err = json.NewDecoder(io.LimitReader(connection, 8<<20)).Decode(&reply)
		connection.Close()
		if err != nil || subtle.ConstantTimeCompare([]byte(reply.Token), []byte(token)) != 1 {
			continue
		}
		if reply.Code != 0 && reply.Code != 1 || !json.Valid(reply.Output) {
			return 1, nil, errors.New("invalid independent lifecycle result")
		}
		return reply.Code, reply.Output, nil
	}
}
