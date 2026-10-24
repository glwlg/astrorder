package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"astrorder.dev/app-server/internal/agentskill"
)

func runAgentSkill(args []string, output io.Writer) int {
	if len(args) < 1 {
		return cliFailure(output, errors.New("usage: astrorderctl agent-skill install --runtime hermes|codex|grok --home PATH --cli PATH --agent-id ID --connection-id ID --profile-name ID"))
	}
	switch args[0] {
	case "install":
		return runAgentSkillInstall(args[1:], output)
	default:
		return cliFailure(output, fmt.Errorf("unknown agent-skill command %q", args[0]))
	}
}

func runAgentSkillInstall(args []string, output io.Writer) int {
	flags := flag.NewFlagSet("agent-skill install", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	runtime := flags.String("runtime", "", "target agent runtime: hermes, codex or grok")
	home := flags.String("home", "", "absolute home directory of agent")
	cli := flags.String("cli", "", "absolute path to verified astrorder-agent executable")
	agentID := flags.String("agent-id", "", "caller agent ID")
	connID := flags.String("connection-id", "", "caller connection ID")
	profName := flags.String("profile-name", "", "caller profile name")

	if err := flags.Parse(args); err != nil {
		return cliFailure(output, err)
	}
	if flags.NArg() != 0 {
		return cliFailure(output, errors.New("unexpected agent-skill install positional arguments"))
	}

	result, err := agentskill.Install(agentskill.InstallOptions{
		Runtime:      *runtime,
		Home:         *home,
		CLI:          *cli,
		AgentID:      *agentID,
		ConnectionID: *connID,
		ProfileName:  *profName,
	})
	if err != nil {
		return cliFailure(output, err)
	}
	return cliSuccess(output, result)
}
