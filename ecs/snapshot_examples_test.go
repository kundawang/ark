package ecs_test

import "github.com/mlange-42/ark/ecs"

func ExampleWorld_Snapshot() {
	// Create a world with a component and a resource.
	world := ecs.NewWorld()
	positions := ecs.NewMap[Position](world)
	ecs.AddResource(world, &Grid{Width: 100, Height: 100})

	entity := positions.NewEntity(&Position{X: 1, Y: 2})

	// Take a checkpoint, e.g. before running a simulation step.
	checkpoint := world.Snapshot()

	// Simulate: mutate components, resources and entities.
	positions.Get(entity).X += 10
	ecs.GetResource[Grid](world).Width = 200
	world.RemoveEntity(entity)

	// Restore the checkpoint.
	// Entities, components and resources are back to the state of the snapshot.
	if err := world.Restore(checkpoint); err != nil {
		panic(err)
	}

	pos := positions.Get(entity)
	println(pos.X, pos.Y, ecs.GetResource[Grid](world).Width)
	// Output:
}

func ExampleWorld_Restore() {
	// Create a world and an entity.
	world := ecs.NewWorld()
	positions := ecs.NewMap[Position](world)
	entity := positions.NewEntity(&Position{X: 1, Y: 2})

	// Save a snapshot.
	snapshot := world.Snapshot()

	// Change the world.
	positions.Get(entity).X = 42

	// Restore the snapshot.
	world.MustRestore(snapshot)

	println(positions.Get(entity).X)
	// Output:
}
