package envx

import (
	"strings"

	"github.com/joho/godotenv"
)

// parseDotEnv parses .env contents into a map with godotenv, never touching
// os.Environ.
func parseDotEnv(data string) (map[string]string, error) {
	// Editors on Windows write a BOM, and it is invisible, so the first key
	// would otherwise fail to parse for no reason the author can see.
	m, err := godotenv.Unmarshal(strings.TrimPrefix(data, "\ufeff"))
	if err != nil {
		return nil, err
	}
	return m, nil
}
