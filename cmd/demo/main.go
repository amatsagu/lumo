package main

import (
	"errors"
	"fmt"

	"github.com/amatsagu/lumo"
)

// Demo Struct
type Player struct {
	ID    int
	Name  string
	Stats map[string]int
}

func (p Player) String() string {
	return fmt.Sprintf("%s (Lvl %d)", p.Name, p.Stats["level"])
}

func main() {
	lumo.EnableDebug()
	lumo.HidePackagePrefix()
	lumo.Info("Initializing Lumo visual test...")
	lumo.Debug("Loaded configuration from %s", "/etc/config.yaml")

	lumo.Warn("Simulating high-speed log burst...")
	for i := range 3 {
		lumo.Info("Processing item #%d...", i+1)
	}

	player := Player{
		ID:    107,
		Name:  "Garen",
		Stats: map[string]int{"level": 5, "hp": 100},
	}

	if err := performAction(player); err != nil {
		lumo.Panic("Game Loop Crashed: %v", err)
	}

	// Flush before exit
	lumo.Close()
}

func performAction(p Player) error {
	return calculateDamage(p)
}

func calculateDamage(p Player) error {
	// Simulate an external error
	err := errors.New("buffer overflow in physics engine")

	// Wrap it to add stack trace & context
	lErr := lumo.WrapError(err)
	lErr.Include("player", p) // Behaves like fmt formatting
	lErr.Include("raw_stats", p.Stats)
	lErr.Include("tick", 14002)

	return lErr
}
