// Command issuer-demo demonstrates the production split locally: Go is the
// issuer control plane and Solidity is the independently readable verifier.
// It only accepts the known local Hardhat network and must never be used with
// a production private key.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

type issueRequest struct {
	LearnerID     string
	SkillID       string
	Evidence      string
	Progress      int
	Milestone     string
	HolderAddress string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	var (
		result any
		err    error
	)
	switch os.Args[1] {
	case "network":
		err = runNetwork(ctx)
	case "issue":
		request, parseErr := parseIssueFlags(os.Args[2:])
		if parseErr != nil {
			err = parseErr
			break
		}
		result, err = issueOnHardhat(ctx, request)
	case "verify":
		request, parseErr := parseVerifyFlags(os.Args[2:])
		if parseErr != nil {
			err = parseErr
			break
		}
		result, err = verifyOnHardhat(ctx, request)
	case "sign-project":
		request, parseErr := parseProjectProofFlags(os.Args[2:], false)
		if parseErr != nil {
			err = parseErr
			break
		}
		result, err = signProject(ctx, request)
	case "verify-project":
		request, parseErr := parseProjectProofFlags(os.Args[2:], true)
		if parseErr != nil {
			err = parseErr
			break
		}
		result, err = verifyProject(ctx, request)
	case "sign-telemetry":
		request, parseErr := parseTelemetryFlags(os.Args[2:], false)
		if parseErr != nil {
			err = parseErr
			break
		}
		result, err = signTelemetry(ctx, request)
	case "verify-telemetry":
		request, parseErr := parseTelemetryFlags(os.Args[2:], true)
		if parseErr != nil {
			err = parseErr
			break
		}
		result, err = verifyTelemetry(ctx, request)
	case "upgrade":
		request, parseErr := parseUpgradeFlags(os.Args[2:])
		if parseErr != nil {
			err = parseErr
			break
		}
		result, err = upgradeOnHardhat(ctx, request)
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "issuer demo:", err)
		os.Exit(1)
	}
	if result != nil {
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			fmt.Fprintln(os.Stderr, "issuer demo output:", err)
			os.Exit(1)
		}
	}
}

func parseProjectProofFlags(arguments []string, requireSignature bool) (projectProofRequest, error) {
	flags := flag.NewFlagSet("project proof", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	registry := flags.String("registry", "", "SkillMVPRegistry address")
	attestation := flags.String("attestation", "", "active skill attestation ID")
	evidence := flags.String("evidence", "", "sanitized project evidence")
	signature := flags.String("signature", "", "holder signature from sign-project")
	if err := flags.Parse(arguments); err != nil {
		return projectProofRequest{}, err
	}
	if requireSignature && *signature == "" {
		return projectProofRequest{}, fmt.Errorf("signature is required")
	}
	return projectProofRequest{Registry: *registry, Attestation: *attestation, Evidence: *evidence, Signature: *signature}, nil
}

func parseIssueFlags(arguments []string) (issueRequest, error) {
	flags := flag.NewFlagSet("issue", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	learnerID := flags.String("learner-id", "", "issuer learner identifier")
	skillID := flags.String("skill-id", "rag-ai", "skill being certified")
	evidence := flags.String("evidence", "", "sanitized project evidence")
	progress := flags.Int("progress", 100, "completed progress from 0 to 100")
	milestone := flags.String("milestone", "completed", "sanitized project milestone")
	holderAddress := flags.String("holder-address", "", "developer wallet address to bind to this skill")
	if err := flags.Parse(arguments); err != nil {
		return issueRequest{}, err
	}
	return issueRequest{LearnerID: *learnerID, SkillID: *skillID, Evidence: *evidence, Progress: *progress, Milestone: *milestone, HolderAddress: *holderAddress}, nil
}

func parseVerifyFlags(arguments []string) (verifyRequest, error) {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	registry := flags.String("registry", "", "SkillMVPRegistry address from issue output")
	learnerID := flags.String("learner-id", "", "issuer learner identifier")
	skillID := flags.String("skill-id", "rag-ai", "skill to verify")
	if err := flags.Parse(arguments); err != nil {
		return verifyRequest{}, err
	}
	return verifyRequest{Registry: *registry, LearnerID: *learnerID, SkillID: *skillID}, nil
}

func parseUpgradeFlags(arguments []string) (upgradeRequest, error) {
	flags := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	registry := flags.String("registry", "", "SkillMVPRegistry address from issue output")
	predecessor := flags.String("predecessor", "", "prior attestation ID from issue output")
	skillID := flags.String("skill-id", "rag-ai", "skill being updated")
	evidence := flags.String("evidence", "", "sanitized new project evidence")
	progress := flags.Int("progress", 100, "updated progress from 0 to 100")
	milestone := flags.String("milestone", "policy-updated", "sanitized updated milestone")
	holderAddress := flags.String("holder-address", "", "developer wallet address to bind (optional)")
	if err := flags.Parse(arguments); err != nil {
		return upgradeRequest{}, err
	}
	return upgradeRequest{Registry: *registry, Predecessor: *predecessor, SkillID: *skillID, Evidence: *evidence, Progress: *progress, Milestone: *milestone, HolderAddress: *holderAddress}, nil
}

func usage(output *os.File) {
	_, _ = fmt.Fprintln(output, "usage: issuer-demo network | issue | verify | sign-project | verify-project | sign-telemetry | verify-telemetry | upgrade")
	_, _ = fmt.Fprintln(output, "  network                                      start local Hardhat and wait")
	_, _ = fmt.Fprintln(output, "  issue -learner-id ID -evidence TEXT         issue a skill through Go")
	_, _ = fmt.Fprintln(output, "  verify -registry ADDRESS -learner-id ID     read the Solidity contract only")
	_, _ = fmt.Fprintln(output, "  sign-project -registry ADDRESS -attestation ID -evidence TEXT")
	_, _ = fmt.Fprintln(output, "  verify-project -registry ADDRESS -attestation ID -evidence TEXT -signature HEX")
	_, _ = fmt.Fprintln(output, "  sign-telemetry -registry ADDRESS -attestation ID -metric-name TEXT -metric-value NUM")
	_, _ = fmt.Fprintln(output, "  verify-telemetry -registry ADDRESS -attestation ID -metric-name TEXT -metric-value NUM -signature HEX")
	_, _ = fmt.Fprintln(output, "  upgrade -registry ADDRESS -predecessor ID -evidence TEXT")
}
