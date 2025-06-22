package main

import (
	"encoding/json"
	"io"
	"os"
	yapyap "yapyap/models"
)

func main() {

	user := yapyap.User{}
	stdout := io.Writer(os.Stdout)
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")

	encoder.Encode(user)

}
