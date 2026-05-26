package ark

import (
	"fmt"
	"strings"
)

// DMSender is the sender identity for ComposeDM. Exactly one of
// Session or Service must be set; ComposeDM enforces the XOR.
// CRC: crc-CLI.md | R2722
type DMSender struct {
	Session string // session UUID — emits `@from: <session>`
	Service string // service identity (e.g. ARK-RECALL) — emits `@from-service: <name>`
}

// Identity returns the tmp:// sender segment.
// CRC: crc-CLI.md | R2724
func (s DMSender) Identity() string {
	if s.Session != "" {
		return s.Session
	}
	return s.Service
}

// ComposeDM builds one direct-message chunk: tmp:// destination path
// and the chunk content (leading boundary newline + tag block + body
// + trailing newline). Shared between the `ark message dm` CLI and
// in-process callers such as the simple-recall watcher.
//
// CRC: crc-CLI.md | Seq: seq-message.md | R2716, R2717, R2718, R2719, R2720, R2721, R2722, R2723, R2724, R2725, R2726, R2727
func ComposeDM(sender DMSender, recipients []string, subject, ref, body string) (path, content string, err error) {
	if sender.Session == "" && sender.Service == "" {
		return "", "", fmt.Errorf("dm: --from or --from-service required")
	}
	if sender.Session != "" && sender.Service != "" {
		return "", "", fmt.Errorf("dm: --from and --from-service are mutually exclusive")
	}
	if len(recipients) == 0 {
		return "", "", fmt.Errorf("dm: at least one --to recipient required")
	}
	for _, r := range recipients {
		if r == "" || strings.ContainsAny(r, " \t\n") {
			return "", "", fmt.Errorf("dm: recipient %q must be a single non-whitespace token", r)
		}
	}
	if subject != "" && strings.TrimSpace(subject) == "" {
		return "", "", fmt.Errorf("dm: --subject text must not be empty")
	}

	var buf strings.Builder
	buf.WriteString("\n@dm: ")
	buf.WriteString(strings.Join(recipients, " "))
	if subject != "" {
		buf.WriteString(": ")
		buf.WriteString(subject)
	}
	if sender.Session != "" {
		buf.WriteString("\n@from: ")
		buf.WriteString(sender.Session)
	} else {
		buf.WriteString("\n@from-service: ")
		buf.WriteString(sender.Service)
	}
	if ref != "" {
		buf.WriteString("\n@ref: ")
		buf.WriteString(ref)
	}
	buf.WriteString("\n")
	buf.WriteString(body)
	buf.WriteString("\n")

	path = fmt.Sprintf("tmp://%s/dm-%s", sender.Identity(), recipients[0])
	return path, buf.String(), nil
}
