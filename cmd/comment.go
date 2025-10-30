package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/chmouel/gh-prreview/pkg/github"
	"github.com/chmouel/gh-prreview/pkg/ui"
	"github.com/spf13/cobra"
)

var (
	commentBody     string
	commentBodyFile string
	commentUseStdin bool
	commentDebug    bool
	commentResolve  bool
)

var commentCmd = &cobra.Command{
	Use:   "comment [PR_NUMBER] COMMENT_ID",
	Short: "Reply to a pull request review comment",
	Long: `Post a reply to an existing pull request review comment thread.

When PR_NUMBER is omitted, the PR for the current branch is used.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runComment,
}

func init() {
	commentCmd.Flags().StringVar(&commentBody, "body", "", "Comment body to post")
	commentCmd.Flags().StringVar(&commentBodyFile, "body-file", "", "Path to file containing the comment body")
	commentCmd.Flags().BoolVar(&commentUseStdin, "stdin", false, "Read the comment body from standard input")
	commentCmd.Flags().BoolVar(&commentDebug, "debug", false, "Enable debug output")
	commentCmd.Flags().BoolVar(&commentResolve, "resolve", false, "Resolve the comment thread after replying")
}

func runComment(cmd *cobra.Command, args []string) error {
	client := github.NewClient()
	client.SetDebug(commentDebug)
	if repoFlag != "" {
		client.SetRepo(repoFlag)
	}

	var (
		prNumber       int
		commentIDInput string
		err            error
	)

	if len(args) == 1 {
		prNumber, err = client.GetCurrentBranchPR()
		if err != nil {
			return err
		}
		commentIDInput = args[0]
	} else {
		prNumber, err = strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid PR number: %s", args[0])
		}
		commentIDInput = args[1]
	}

	commentID, err := strconv.ParseInt(commentIDInput, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid comment ID: %s", commentIDInput)
	}

	body, err := resolveCommentBody()
	if err != nil {
		return err
	}

	reply, err := client.ReplyToReviewComment(prNumber, commentID, body)
	if err != nil {
		return err
	}

	link := reply.HTMLURL
	if link == "" {
		link = fmt.Sprintf("https://github.com/%s/pull/%d#discussion_r%d", getRepoFromClient(client), prNumber, reply.ID)
	}

	fmt.Printf("%s Reply posted by @%s: %s\n",
		ui.Colorize(ui.ColorGreen, "✓"),
		ui.Colorize(ui.ColorCyan, reply.Author),
		ui.CreateHyperlink(link, fmt.Sprintf("comment %d", reply.ID)))

	// Resolve the thread if --resolve flag is set
	if commentResolve {
		// Fetch the thread ID for this comment
		comments, err := client.FetchReviewComments(prNumber)
		if err != nil {
			return fmt.Errorf("failed to fetch review comments: %w", err)
		}

		var threadID string
		for _, c := range comments {
			if c.ID == commentID {
				threadID = c.ThreadID
				break
			}
		}

		if threadID == "" {
			return fmt.Errorf("comment ID %d not found in PR #%d", commentID, prNumber)
		}

		if err := client.ResolveThread(threadID); err != nil {
			return fmt.Errorf("failed to resolve thread: %w", err)
		}

		fmt.Printf("%s Thread marked as resolved\n",
			ui.Colorize(ui.ColorGreen, "✓"))
	}

	return nil
}

func resolveCommentBody() (string, error) {
	selected := 0
	if commentBody != "" {
		selected++
	}
	if commentBodyFile != "" {
		selected++
	}
	if commentUseStdin {
		selected++
	}

	if selected > 1 {
		return "", errors.New("only one of --body, --body-file, or --stdin may be used")
	}

	switch {
	case commentBody != "":
		return strings.TrimSpace(commentBody), nil
	case commentBodyFile != "":
		content, err := os.ReadFile(commentBodyFile)
		if err != nil {
			return "", fmt.Errorf("failed to read body file: %w", err)
		}
		return sanitizeComment(string(content), false)
	case commentUseStdin:
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("failed to read from stdin: %w", err)
		}
		return sanitizeComment(string(data), false)
	default:
		return promptForCommentBody()
	}
}

func promptForCommentBody() (string, error) {
	template := "# Write your PR review comment above. Lines starting with # are ignored.\n"

	tmpFile, err := os.CreateTemp("", "gh-prreview-comment-*.md")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary file: %w", err)
	}
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	if _, err := tmpFile.WriteString(template); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("failed to write template: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("failed to close temporary file: %w", err)
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	editorParts := strings.Fields(editor)
	if len(editorParts) == 0 {
		return "", fmt.Errorf("invalid EDITOR value: %q", editor)
	}

	editorCmd := exec.Command(editorParts[0], append(editorParts[1:], tmpFile.Name())...)
	editorCmd.Stdin = os.Stdin
	editorCmd.Stdout = os.Stdout
	editorCmd.Stderr = os.Stderr

	if err := editorCmd.Run(); err != nil {
		return "", fmt.Errorf("editor exited with error: %w", err)
	}

	content, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		return "", fmt.Errorf("failed to read editor content: %w", err)
	}

	return sanitizeComment(string(content), true)
}

func sanitizeComment(raw string, stripCommentLines bool) (string, error) {
	var builder strings.Builder
	scanner := bufio.NewScanner(strings.NewReader(raw))
	firstLine := true

	for scanner.Scan() {
		line := scanner.Text()
		if stripCommentLines && strings.HasPrefix(line, "#") {
			continue
		}
		if firstLine {
			builder.WriteString(line)
			firstLine = false
		} else {
			builder.WriteRune('\n')
			builder.WriteString(line)
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to parse comment body: %w", err)
	}

	body := strings.TrimSpace(builder.String())
	if body == "" {
		return "", errors.New("comment body cannot be empty")
	}

	return body, nil
}
