package lifecycle

import (
	"os/exec"
	"strings"
	"testing"
)

func TestJavaServiceShellSyntax(t *testing.T) {
	for _, id := range []string{"wildfly", "tomcat"} {
		command := exec.Command("sh", "-n")
		command.Stdin = strings.NewReader(javaServiceSetup(id))
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s installer shell syntax: %s %v", id, out, err)
		}
	}
}
