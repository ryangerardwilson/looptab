package loader

import (
	"os"

	"github.com/ryangerardwilson/looptab/internal/config"
	"github.com/ryangerardwilson/looptab/internal/parser"
	"github.com/ryangerardwilson/looptab/internal/paths"
)

func Load(p paths.Paths) (parser.File, error) {
	settings, location, err := config.Load(p)
	if err != nil {
		return parser.File{}, err
	}

	content, err := os.ReadFile(p.ConfigFile)
	if err != nil {
		return parser.File{}, err
	}

	file, err := parser.ParseWithLocation(string(content), settings.Timezone, location)
	if err != nil {
		return parser.File{}, err
	}
	return file, nil
}