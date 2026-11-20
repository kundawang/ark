package snapshots

import (
	"testing"

	"github.com/mlange-42/ark/ecs"
)

var world = ecs.NewWorld()

type Position struct {
	X float64
	Y float64
}

type Velocity struct {
	X float64
	Y float64
}

type Simulation struct {
	Tick int
}

func TestSnapshotBasics(t *testing.T) {
	w := ecs.NewWorld()
	mapper := ecs.NewMap[Position](w)
	e := mapper.NewEntity(&Position{X: 1, Y: 2})

	// Take a snapshot of the entire world.
	snapshot := w.Snapshot()

	// Modify the world.
	mapper.Get(e).X = 100
	w.RemoveEntity(e)

	// Restore the snapshot.
	if err := w.Restore(snapshot); err != nil {
		panic(err)
	}

	// The entity and its component are back.
	if !w.Alive(e) {
		panic("entity should be alive")
	}
	if pos := mapper.Get(e); pos.X != 1 || pos.Y != 2 {
		panic("wrong component values")
	}
}

func TestSnapshotCheckpoints(t *testing.T) {
	w := ecs.NewWorld()
	mapper := ecs.NewMap[Position](w)

	e := mapper.NewEntity(&Position{})

	// Save multiple checkpoints, e.g. during a simulation.
	checkpoints := []*ecs.Snapshot{}
	for i := range 3 {
		mapper.Get(e).X = float64(i)
		checkpoints = append(checkpoints, w.Snapshot())
	}

	// Restore any checkpoint later.
	if err := w.Restore(checkpoints[1]); err != nil {
		panic(err)
	}
	if mapper.Get(e).X != 1 {
		panic("wrong component value")
	}
}

func TestSnapshotResources(t *testing.T) {
	w := ecs.NewWorld()
	ecs.AddResource(w, &Simulation{Tick: 10})

	// Resources are part of a snapshot.
	snapshot := w.Snapshot()

	ecs.GetResource[Simulation](w).Tick = 20
	if err := w.Restore(snapshot); err != nil {
		panic(err)
	}

	if ecs.GetResource[Simulation](w).Tick != 10 {
		panic("wrong resource value")
	}
}

func TestSnapshotRelations(t *testing.T) {
	w := ecs.NewWorld()
	childMap := ecs.NewMap1[ChildOf](w)

	parent := w.NewEntity()
	child := childMap.NewEntity(&ChildOf{}, ecs.Rel[ChildOf](parent))

	// Relation targets are preserved.
	snapshot := w.Snapshot()

	w.RemoveEntity(parent)

	if err := w.Restore(snapshot); err != nil {
		panic(err)
	}

	if !w.Alive(parent) {
		panic("relation target should be alive")
	}
	if childMap.GetRelation(child, 0) != parent {
		panic("wrong relation target")
	}
}

type ChildOf struct {
	ecs.RelationMarker
}
