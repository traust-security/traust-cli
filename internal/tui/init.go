package tui

import (
	"charm.land/huh/v2"
)

// InitAnswers is the outcome of the init wizard.
type InitAnswers struct {
	Workspace      string
	Forge          string
	Ref            string
	Harness        string
	FullPermission bool
}

// RunInitWizard asks the interactive setup questions. detected lists the
// harness binaries found on PATH (id → version hint); defaultHarness is the
// preselected one.
func RunInitWizard(defaultWorkspace, defaultForge string, detected []string, defaultHarness string) (*InitAnswers, error) {
	answers := &InitAnswers{
		Workspace: defaultWorkspace,
		Forge:     defaultForge,
		Ref:       "latest",
		Harness:   defaultHarness,
		// Full permission is the recommended default: the harness's skills
		// cannot run unattended without it, so the confirm highlights Yes.
		FullPermission: true,
	}

	harnessOptions := []huh.Option[string]{}
	for _, id := range detected {
		harnessOptions = append(harnessOptions, huh.NewOption(id, id))
	}
	if len(harnessOptions) == 0 {
		harnessOptions = append(harnessOptions, huh.NewOption("claude (not detected — install it before running)", "claude"))
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Workspace directory").
				Description("Everything the CLI manages lives here: the traust checkout, config, binaries, run journals.").
				Placeholder(defaultWorkspace).
				Value(&answers.Workspace),
			huh.NewInput().
				Title("traust repository").
				Description("git URL to clone the harness from.").
				Placeholder(defaultForge).
				Value(&answers.Forge),
			huh.NewInput().
				Title("Ref to pin").
				Description(`A vX.Y.Z tag, a branch, or "latest" for the newest stable tag.`).
				Placeholder("latest").
				Value(&answers.Ref),
		),
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Default agent harness").
				Description("Detected on PATH. You can switch per run with --harness.").
				Options(harnessOptions...).
				Value(&answers.Harness),
		),
		huh.NewGroup(
			huh.NewConfirm().
				Title("Run skills with full permissions? (recommended)").
				Description("The traust harness's skills REQUIRE unattended tool use (bash, file writes). "+
					"WARNING: without full permission, runs and flows (audit → triage → patch → verify) "+
					"fail closed on the first permission request — most workflows will not complete. "+
					"With it, your harness runs with all tools auto-approved "+
					"(claude --dangerously-skip-permissions, crush --yolo, codex --dangerously-bypass-approvals-and-sandbox). "+
					"S9 isolation still applies: agents run from the harness root and never load the audit target's configuration.").
				Value(&answers.FullPermission),
		),
	)
	if err := form.Run(); err != nil {
		return nil, err
	}
	return answers, nil
}
