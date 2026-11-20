package cli

import (
	"encoding/json"

	"github.com/toxic-sunflower/lite-deploy/internal/project"
)

// cmdProjectExport prints the description of a project as it is kept on the server. A description holds no secret values,
// only references (secret://name); `needed` lists the names the person must type again when the description is imported
// on another server.
func cmdProjectExport(args []string, env Env) result {
	const command = "project.export"
	if len(args) != 1 {
		return fail(command, CodeUsage, "usage: lite-deploy project export <project>")
	}
	b, err := env.FS.ReadFile("/etc/lite-deploy/projects/" + args[0] + ".json")
	if err != nil {
		return fail(command, CodeProjectMissing, "project "+args[0]+" not found")
	}
	if _, issues := project.Parse(b); len(issues) > 0 {
		return fail(command, CodeIO, "the stored description of "+args[0]+" cannot be read: "+issues[0].Message)
	}
	var raw json.RawMessage = b
	return result{command: command, data: map[string]any{"project": raw, "needed": neededSecrets(env, args[0])}, text: string(b)}
}
