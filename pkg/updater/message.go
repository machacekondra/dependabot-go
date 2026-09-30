package updater

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/omachace/dependabot-go/pkg/dependabot"
)

// Message is the text of a pull request.
type Message struct {
	CommitMessage string
	PRName        string
	PRMessage     string
}

// BuildMessage renders the title, commit message and body for a change.
//
// Ruby's PullRequestCreator::MessageBuilder also fetches changelogs, release
// notes, commit lists and upgrade guides from the dependency's source
// repository, and honours the commit-message preferences configured on the
// repo. Only the source URL is used here; the rest needs a host API client
// this port does not have.
func BuildMessage(ctx context.Context, eco dependabot.Ecosystem, job *Job, change *DependencyChange) *Message {
	if len(change.Dependencies) == 0 {
		return &Message{}
	}

	name := prName(change)
	body := prBody(ctx, eco, job, change)

	commit := name
	if body != "" {
		commit = name + "\n\n" + body
	}
	return &Message{CommitMessage: commit, PRName: name, PRMessage: body}
}

func prName(change *DependencyChange) string {
	directory := ""
	if change.Directory != "" && change.Directory != "/" {
		directory = " in " + change.Directory
	}

	if len(change.Dependencies) == 1 {
		dep := change.Dependencies[0]
		from := dep.HumanizedPreviousVersion()
		to := dep.HumanizedVersion()
		switch {
		case dep.Removed:
			return fmt.Sprintf("Remove %s%s", dep.DisplayName(), directory)
		case from == "" || to == "":
			return fmt.Sprintf("Update %s requirement%s", dep.DisplayName(), directory)
		default:
			return fmt.Sprintf("Bump %s from %s to %s%s", dep.DisplayName(), from, to, directory)
		}
	}

	names := make([]string, 0, len(change.Dependencies))
	for _, dep := range change.Dependencies {
		names = append(names, dep.DisplayName())
	}
	sort.Strings(names)
	return fmt.Sprintf("Bump %s%s", humanizeList(names), directory)
}

func prBody(ctx context.Context, eco dependabot.Ecosystem, job *Job, change *DependencyChange) string {
	var b strings.Builder
	for i, dep := range change.Dependencies {
		if i > 0 {
			b.WriteString("\n")
		}

		from := dep.HumanizedPreviousVersion()
		to := dep.HumanizedVersion()
		switch {
		case dep.Removed:
			fmt.Fprintf(&b, "Removes %s.\n", dep.DisplayName())
		case from != "" && to != "":
			fmt.Fprintf(&b, "Bumps %s from %s to %s.\n", dep.DisplayName(), from, to)
		default:
			fmt.Fprintf(&b, "Updates the requirements on %s.\n", dep.DisplayName())
		}

		if url := sourceURL(ctx, eco, job, dep); url != "" {
			fmt.Fprintf(&b, "\nSource: %s\n", url)
		}
	}
	return b.String()
}

// sourceURL asks the ecosystem's metadata finder where the dependency lives.
// A failure is not worth reporting: the body is simply less informative.
func sourceURL(ctx context.Context, eco dependabot.Ecosystem, job *Job, dep *dependabot.Dependency) string {
	finder, err := eco.NewMetadataFinder(dependabot.MetadataConfig{
		Dependency:  dep,
		Credentials: job.Credentials,
	})
	if err != nil {
		return ""
	}
	url, err := finder.SourceURL(ctx)
	if err != nil {
		return ""
	}
	return url
}

func humanizeList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}
