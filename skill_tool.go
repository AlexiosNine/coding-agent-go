package cc

import (
	"context"
	"fmt"
)

type useSkillInput struct {
	Name   string `json:"name" desc:"Skill name to activate or deactivate"`
	Action string `json:"action" desc:"'activate' to enable a skill, 'deactivate' to disable it (default: activate)"`
}

// UseSkillTool creates a tool that lets the model activate/deactivate skills.
func UseSkillTool(registry *SkillRegistry) Tool {
	return NewFuncTool(
		"use_skill",
		"Activate or deactivate a skill. Use 'activate' to load a skill's instructions and tools. Use 'deactivate' to release them. Available skills are listed in the system prompt.",
		func(ctx context.Context, input useSkillInput) (string, error) {
			if input.Name == "" {
				return "", fmt.Errorf("skill name is required")
			}

			action := input.Action
			if action == "" {
				action = "activate"
			}

			switch action {
			case "activate":
				if err := registry.Activate(input.Name); err != nil {
					return "", err
				}
				skill := registry.skills[input.Name]
				return fmt.Sprintf("Skill %q activated. Follow its instructions:\n%s", input.Name, skill.Instructions), nil

			case "deactivate":
				registry.Deactivate(input.Name)
				return fmt.Sprintf("Skill %q deactivated.", input.Name), nil

			default:
				return "", fmt.Errorf("unknown action %q (use 'activate' or 'deactivate')", action)
			}
		},
	)
}
