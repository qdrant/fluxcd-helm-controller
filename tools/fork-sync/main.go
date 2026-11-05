// fork-sync brings the releases of a fork's upstream into the fork, as pull
// requests. See the cmd package for its commands.
package main

import "github.com/qdrant/fluxcd-helm-controller/tools/fork-sync/cmd"

func main() {
	cmd.Execute()
}
