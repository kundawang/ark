package ecs

import (
	"errors"
	"reflect"
	"testing"
)

// Tests for world state snapshots and restore.

func TestSnapshotEmptyWorld(t *testing.T) {
	w := NewWorld(16)

	snapshot := w.Snapshot()
	expectNotNil(t, snapshot)

	// Restoring an empty snapshot into an empty world must be a no-op.
	expectNil(t, w.Restore(snapshot))

	query := NewUnsafeFilter(w).Query()
	expectEqual(t, 0, query.Count())
	query.Close()

	// Restoring an empty snapshot must remove all entities and resources.
	e := w.NewEntity()
	AddResource(w, &Position{1, 2})
	expectNil(t, w.Restore(snapshot))

	expectFalse(t, w.Alive(e))
	expectNil(t, GetResource[Position](w))

	query = NewUnsafeFilter(w).Query()
	expectEqual(t, 0, query.Count())
	query.Close()

	// The world must still be usable.
	e2 := w.NewEntity()
	expectTrue(t, w.Alive(e2))
}

func TestSnapshotRoundTrip(t *testing.T) {
	w := NewWorld(16)

	posMap := NewMap[Position](w)
	velMap := NewMap[Velocity](w)

	e1 := posMap.NewEntity(&Position{1, 2})
	e2 := velMap.NewEntity(&Velocity{3, 4})
	posMap.Add(e2, &Position{9, 10})
	e3 := posMap.NewEntity(&Position{5, 6})
	velMap.Add(e3, &Velocity{7, 8})

	snapshot := w.Snapshot()

	// Modify the world after taking the snapshot.
	posMap.Get(e1).X = 100
	velMap.Add(e1, &Velocity{9, 9})
	posMap.Remove(e2)
	w.RemoveEntity(e3)

	expectNil(t, w.Restore(snapshot))

	expectTrue(t, w.Alive(e1))
	expectTrue(t, w.Alive(e2))
	expectTrue(t, w.Alive(e3))

	expectEqual(t, Position{1, 2}, *posMap.Get(e1))
	expectFalse(t, velMap.Has(e1))
	expectEqual(t, Position{9, 10}, *posMap.Get(e2))
	expectEqual(t, Velocity{3, 4}, *velMap.Get(e2))
	expectEqual(t, Position{5, 6}, *posMap.Get(e3))
	expectEqual(t, Velocity{7, 8}, *velMap.Get(e3))

	// Queries must reflect the restored state.
	posQuery := NewFilter1[Position](w).Query()
	expectEqual(t, 3, posQuery.Count())
	posQuery.Close()

	velQuery := NewFilter1[Velocity](w).Query()
	expectEqual(t, 2, velQuery.Count())
	velQuery.Close()
}

func TestSnapshotIndependentOfWorld(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)

	e := posMap.NewEntity(&Position{1, 2})
	snapshot := w.Snapshot()

	// Changing the world must not affect the snapshot.
	posMap.Get(e).X = 42
	w.RemoveEntity(e)
	expectNil(t, w.Restore(snapshot))
	expectEqual(t, Position{1, 2}, *posMap.Get(e))

	// Changing the world again and restoring twice must give the same result.
	posMap.Get(e).Y = 99
	expectNil(t, w.Restore(snapshot))
	expectEqual(t, Position{1, 2}, *posMap.Get(e))
	expectNil(t, w.Restore(snapshot))
	expectEqual(t, Position{1, 2}, *posMap.Get(e))
}

func TestSnapshotMultipleCheckpoints(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)
	velMap := NewMap[Velocity](w)

	e := posMap.NewEntity(&Position{0, 0})

	checkpoints := make([]*Snapshot, 3)
	velMap.Add(e, &Velocity{})
	for i := range checkpoints {
		posMap.Get(e).X = float64((i + 1) * 10)
		*velMap.Get(e) = Velocity{X: float64(i), Y: float64(i)}
		checkpoints[i] = w.Snapshot()
	}

	// Restore all checkpoints in reverse order.
	for i := len(checkpoints) - 1; i >= 0; i-- {
		expectNil(t, w.Restore(checkpoints[i]))
		expectEqual(t, Position{float64((i + 1) * 10), 0}, *posMap.Get(e))
		expectEqual(t, Velocity{X: float64(i), Y: float64(i)}, *velMap.Get(e))
	}

	// Restore the first checkpoint again, after removing the entity.
	w.RemoveEntity(e)
	expectNil(t, w.Restore(checkpoints[0]))
	query := NewFilter1[Position](w).Query()
	expectEqual(t, 1, query.Count())
	for query.Next() {
		expectEqual(t, Position{10, 0}, *posMap.Get(query.Entity()))
	}
	query.Close()
}

func TestSnapshotResources(t *testing.T) {
	w := NewWorld(16)
	AddResource(w, &Position{1, 2})
	AddResource(w, &Velocity{3, 4})

	snapshot := w.Snapshot()

	// Modify and add resources after the snapshot.
	*GetResource[Position](w) = Position{10, 20}
	AddResource(w, &Heading{5})

	expectNil(t, w.Restore(snapshot))

	expectEqual(t, Position{1, 2}, *GetResource[Position](w))
	expectEqual(t, Velocity{3, 4}, *GetResource[Velocity](w))
	expectNil(t, GetResource[Heading](w))

	// The restored resource must be independent of the snapshot's internal copy.
	GetResource[Position](w).X = 100
	expectNil(t, w.Restore(snapshot))
	expectEqual(t, Position{1, 2}, *GetResource[Position](w))
}

func TestSnapshotResourceIndependentPointers(t *testing.T) {
	w := NewWorld(16)
	res := &Position{1, 2}
	AddResource(w, res)

	snapshot := w.Snapshot()
	expectNil(t, w.Restore(snapshot))

	restored := GetResource[Position](w)
	if restored == res {
		t.Error("expected restored resource to use a new pointer")
	}
	restored.X = 5
	expectEqual(t, float64(1), res.X)
}

func TestSnapshotRelations(t *testing.T) {
	w := NewWorld(16)

	childMap := NewMap[ChildOf](w)
	posMap := NewMap[Position](w)

	parent1 := w.NewEntity()
	parent2 := w.NewEntity()

	child1 := childMap.NewEntity(&ChildOf{}, parent1)
	posMap.Add(child1, &Position{1, 2})
	child2 := childMap.NewEntity(&ChildOf{}, parent2)
	child3 := childMap.NewEntity(&ChildOf{}, parent1)

	snapshot := w.Snapshot()

	// Change relations and remove targets after taking the snapshot.
	childMap.SetRelation(child1, parent2)
	w.RemoveEntity(parent2)
	w.RemoveEntity(child2)
	extra := w.NewEntity()

	expectNil(t, w.Restore(snapshot))

	expectTrue(t, w.Alive(parent1))
	expectTrue(t, w.Alive(parent2))
	expectTrue(t, w.Alive(child1))
	expectTrue(t, w.Alive(child2))
	expectTrue(t, w.Alive(child3))
	expectFalse(t, w.Alive(extra))

	expectEqual(t, parent1, childMap.GetRelation(child1))
	expectEqual(t, parent2, childMap.GetRelation(child2))
	expectEqual(t, parent1, childMap.GetRelation(child3))

	query1 := NewFilter1[ChildOf](w).Query(Rel[ChildOf](parent1))
	expectEqual(t, 2, query1.Count())
	query1.Close()

	query2 := NewFilter1[ChildOf](w).Query(Rel[ChildOf](parent2))
	expectEqual(t, 1, query2.Count())
	query2.Close()

	// Removing a restored relation target must still cleanup relation tables.
	w.RemoveEntity(parent1)
	expectTrue(t, w.Alive(child1))
	expectEqual(t, Entity{}, childMap.GetRelation(child1))
	expectEqual(t, Entity{}, childMap.GetRelation(child3))

	// The children are not removed, only detached from their target.
	childQuery := NewFilter1[ChildOf](w).Query()
	expectEqual(t, 3, childQuery.Count())
	childQuery.Close()

	// The detached target's tables are gone; querying the remaining target works.
	query2 = NewFilter1[ChildOf](w).Query(Rel[ChildOf](parent2))
	expectEqual(t, 1, query2.Count())
	query2.Close()
}

func TestSnapshotMultipleRelations(t *testing.T) {
	w := NewWorld(16)

	mapper := NewMap2[ChildOf, ChildOf2](w)
	parentA := w.NewEntity()
	parentB := w.NewEntity()

	e := mapper.NewEntity(&ChildOf{}, &ChildOf2{}, RelIdx(0, parentA), RelIdx(1, parentB))

	snapshot := w.Snapshot()

	mapper.SetRelations(e, RelIdx(0, parentB), RelIdx(1, parentA))

	expectNil(t, w.Restore(snapshot))

	expectEqual(t, parentA, mapper.GetRelation(e, 0))
	expectEqual(t, parentB, mapper.GetRelation(e, 1))
}

func TestSnapshotEntityReferencesInComponents(t *testing.T) {
	w := NewWorld(16)

	posMap := NewMap[Position](w)
	refMap := NewMap[EntityRef](w)

	target := w.NewEntity()
	holder := refMap.NewEntity(&EntityRef{Target: target})
	posMap.Add(holder, &Position{1, 2})
	AddResource(w, &EntityRef{Target: target})

	snapshot := w.Snapshot()

	w.RemoveEntity(target)
	w.RemoveEntity(holder)
	GetResource[EntityRef](w).Target = Entity{}

	expectNil(t, w.Restore(snapshot))

	expectTrue(t, w.Alive(target))
	expectTrue(t, w.Alive(holder))
	expectEqual(t, target, refMap.Get(holder).Target)
	expectEqual(t, target, GetResource[EntityRef](w).Target)
}

func TestSnapshotEntityRecycling(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)

	e1 := posMap.NewEntity(&Position{1, 1})
	e2 := posMap.NewEntity(&Position{2, 2})
	snapshot := w.Snapshot()

	// Remove and recycle IDs after the snapshot.
	// Recycling is LIFO, so the most recently removed entity's ID is reused first.
	w.RemoveEntity(e1)
	w.RemoveEntity(e2)
	e3 := w.NewEntity()
	expectEqual(t, e2.ID(), e3.ID())
	expectTrue(t, e3.Gen() > e2.Gen())

	expectNil(t, w.Restore(snapshot))

	expectTrue(t, w.Alive(e1))
	expectTrue(t, w.Alive(e2))
	expectFalse(t, w.Alive(e3))
	expectEqual(t, Position{1, 1}, *posMap.Get(e1))
	expectEqual(t, Position{2, 2}, *posMap.Get(e2))

	// The world continues allocating after the restored pool state.
	reused := w.NewEntity()
	expectEqual(t, entityID(4), entityID(reused.ID()))
	expectEqual(t, uint32(0), reused.Gen())

	w.RemoveEntity(reused)
	again := w.NewEntity()
	expectEqual(t, reused.ID(), again.ID())
	expectEqual(t, reused.Gen()+1, again.Gen())
}

func TestSnapshotDeadEntityHandles(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)

	// Create, remove and recycle an entity to get a dead handle of gen 0.
	old := posMap.NewEntity(&Position{1, 2})
	w.RemoveEntity(old)

	snapshot := w.Snapshot()
	expectNil(t, w.Restore(snapshot))

	expectFalse(t, w.Alive(old))

	expectPanicsWithValue(t, "can't get a component of a dead entity", func() {
		_ = posMap.Get(old)
	})
	expectPanicsWithValue(t, "can't remove a dead entity", func() {
		w.RemoveEntity(old)
	})
	expectPanicsWithValue(t, "can't add a component to a dead entity", func() {
		posMap.Add(old, &Position{})
	})
}

func TestSnapshotRestoreLockedWorld(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)
	posMap.NewEntity(&Position{1, 2})
	snapshot := w.Snapshot()

	query := NewFilter1[Position](w).Query()
	expectTrue(t, query.Next())

	err := w.Restore(snapshot)
	expectNotNil(t, err)
	expectTrue(t, errors.Is(err, ErrSnapshotLocked))

	// The world must be unchanged and query iteration must continue to work.
	expectTrue(t, w.IsLocked())
	query.Close()
	expectFalse(t, w.IsLocked())

	query = NewFilter1[Position](w).Query()
	expectEqual(t, 1, query.Count())
	query.Close()

	expectNil(t, w.Restore(snapshot))
}

func TestSnapshotInvalid(t *testing.T) {
	w := NewWorld(16)

	expectTrue(t, errors.Is(w.Restore(nil), ErrInvalidSnapshot))
	expectTrue(t, errors.Is((*Snapshot)(nil).Restore(w), ErrInvalidSnapshot))
	expectTrue(t, errors.Is((*Snapshot)(nil).Restore(nil), ErrInvalidSnapshot))

	var snapshot Snapshot
	expectTrue(t, errors.Is(w.Restore(&snapshot), ErrInvalidSnapshot))
}

func TestSnapshotCorruptData(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)
	e := posMap.NewEntity(&Position{1, 2})
	AddResource(w, &Velocity{1, 2})
	snapshot := w.Snapshot()

	expectEqual(t, 1, len(snapshot.tables))
	expectEqual(t, 1, len(snapshot.resources))

	// Corrupted entity in a table.
	corrupt := cloneSnapshot(snapshot)
	corrupt.tables[0].entities[0] = Entity{id: entityID(e.ID() + 100)}
	expectTrue(t, errors.Is(w.Restore(corrupt), ErrSnapshotEntity))

	// Reserved zero entity in a table.
	corrupt = cloneSnapshot(snapshot)
	corrupt.tables[0].entities[0] = Entity{}
	expectTrue(t, errors.Is(w.Restore(corrupt), ErrSnapshotEntity))

	// Corrupted column length.
	corrupt = cloneSnapshot(snapshot)
	corrupt.tables[0].columns[0] = corrupt.tables[0].columns[0].Slice(0, 0)
	expectTrue(t, errors.Is(w.Restore(corrupt), ErrInvalidSnapshot))

	// Corrupted resource.
	corrupt = cloneSnapshot(snapshot)
	corrupt.resources[0].value = corrupt.resources[0].value.Elem()
	expectTrue(t, errors.Is(w.Restore(corrupt), ErrInvalidSnapshot))

	// A failed restore must leave the world unchanged.
	expectEqual(t, Position{1, 2}, *posMap.Get(e))
	expectEqual(t, Velocity{1, 2}, *GetResource[Velocity](w))
}

func TestSnapshotUnregisteredComponents(t *testing.T) {
	source := NewWorld(16)
	posMap := NewMap[Position](source)
	e := posMap.NewEntity(&Position{1, 2})
	snapshot := source.Snapshot()

	// A fresh world has no component registered.
	target := NewWorld(16)
	expectEqual(t, 0, len(target.storage.registry.Components))

	expectNil(t, target.Restore(snapshot))

	expectEqual(t, 1, len(target.storage.registry.Components))
	posMapTarget := NewMap[Position](target)
	expectTrue(t, target.Alive(e))
	expectEqual(t, Position{1, 2}, *posMapTarget.Get(e))

	// The unregistered component is registered with the same ID as in the source world.
	expectEqual(t, ComponentID[Position](source), ComponentID[Position](target))
}

func TestSnapshotUnregisteredResources(t *testing.T) {
	source := NewWorld(16)
	AddResource(source, &Position{1, 2})
	snapshot := source.Snapshot()

	target := NewWorld(16)
	expectNil(t, target.Restore(snapshot))
	expectEqual(t, Position{1, 2}, *GetResource[Position](target))
}

func TestSnapshotUncopyableResource(t *testing.T) {
	w := NewWorld(16)
	posID := ResourceID[Position](w)

	// Resources must be pointers.
	w.Resources().Add(posID, Position{1, 2})
	_, err := w.TakeSnapshot()
	expectTrue(t, errors.Is(err, ErrSnapshotResource))
	expectPanicsWithValue(t, err.Error(), func() { w.Snapshot() })
	w.Resources().Remove(posID)

	// A nil pointer must be rejected as well.
	w.Resources().Add(posID, (*Position)(nil))
	_, err = w.TakeSnapshot()
	expectTrue(t, errors.Is(err, ErrSnapshotResource))
	w.Resources().Remove(posID)

	// A mismatched pointer type must be rejected.
	w.Resources().Add(posID, &Velocity{1, 2})
	_, err = w.TakeSnapshot()
	expectTrue(t, errors.Is(err, ErrSnapshotResource))
	w.Resources().Remove(posID)

	// After removing the invalid resource, snapshots work again.
	_, err = w.TakeSnapshot()
	expectNil(t, err)
}

func TestSnapshotStructuralChangesAfterRestore(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)
	velMap := NewMap[Velocity](w)

	e1 := posMap.NewEntity(&Position{1, 2})
	e2 := posMap.NewEntity(&Position{3, 4})
	velMap.Add(e2, &Velocity{5, 6})
	snapshot := w.Snapshot()

	expectNil(t, w.Restore(snapshot))

	// Structural changes after the restore must work as usual.
	velMap.Remove(e2)
	velMap.Add(e1, &Velocity{7, 8})
	e3 := w.NewEntity()
	posMap.Add(e3, &Position{9, 10})

	expectEqual(t, Position{1, 2}, *posMap.Get(e1))
	expectEqual(t, Velocity{7, 8}, *velMap.Get(e1))
	expectNil(t, velMap.Get(e2))
	expectTrue(t, w.Alive(e2))
	expectEqual(t, Position{9, 10}, *posMap.Get(e3))

	// Repeating the same sequence after another restore gives the same state.
	expectNil(t, w.Restore(snapshot))
	velMap.Remove(e2)
	velMap.Add(e1, &Velocity{7, 8})
	e3b := w.NewEntity()
	posMap.Add(e3b, &Position{9, 10})
	expectEqual(t, e3, e3b)

	// Entity handles beyond the pool size are dead, not panicking.
	expectFalse(t, w.Alive(Entity{id: 100, gen: 0}))
}

func TestSnapshotRegisteredFilterAfterRestore(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)
	velMap := NewMap[Velocity](w)

	e := posMap.NewEntity(&Position{1, 2})
	snapshot := w.Snapshot()

	velMap.Add(e, &Velocity{3, 4})

	// Register the filter before the restore, so that its cache must be updated.
	filter := NewFilter2[Position, Velocity](w).Register()

	expectNil(t, w.Restore(snapshot))

	query := filter.Query()
	expectEqual(t, 0, query.Count())
	query.Close()

	velMap.Add(e, &Velocity{3, 4})

	query = filter.Query()
	expectEqual(t, 1, query.Count())
	for query.Next() {
		expectEqual(t, e, query.Entity())
	}
	query.Close()
}

func TestSnapshotObservers(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)

	created := 0
	Observe(OnCreateEntity).Do(func(Entity) { created++ }).Register(w)

	e := posMap.NewEntity(&Position{1, 2})
	expectEqual(t, 1, created)

	snapshot := w.Snapshot()
	w.RemoveEntity(e)
	expectFalse(t, w.Alive(e))

	expectNil(t, w.Restore(snapshot))

	// Restoring must not emit events.
	expectEqual(t, 1, created)

	// Observers must still work after a restore.
	posMap.NewEntity(&Position{3, 4})
	expectEqual(t, 2, created)
}

func TestSnapshotSimulation(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)
	velMap := NewMap[Velocity](w)
	childMap := NewMap[ChildOf](w)
	AddResource(w, &Grid{Width: 10, Height: 10})

	// Build a multi-archetype world with relation tables.
	parent1 := w.NewEntity()
	parent2 := w.NewEntity()

	for i := range 8 {
		e := posMap.NewEntity(&Position{X: float64(i), Y: 0})
		velMap.Add(e, &Velocity{X: 1, Y: 2})
	}
	for i := range 4 {
		target := parent1
		if i%2 == 0 {
			target = parent2
		}
		e := childMap.NewEntity(&ChildOf{}, target)
		posMap.Add(e, &Position{X: float64(100 + i), Y: 1})
	}

	// Remove and recycle some entities to exercise generations.
	removed := posMap.NewEntity(&Position{X: 1000})
	w.RemoveEntity(removed)
	recycled := w.NewEntity()
	expectEqual(t, removed.ID(), recycled.ID())
	expectFalse(t, w.Alive(removed))
	expectTrue(t, w.Alive(recycled))

	GetResource[Grid](w).Width = 20

	snapshot := w.Snapshot()

	step := func() {
		GetResource[Grid](w).Width++
		query := NewFilter2[Position, Velocity](w).Query()
		for query.Next() {
			pos, vel := query.Get()
			pos.X += vel.X
			pos.Y += vel.Y
		}
		query.Close()

		// Structural changes: remove an entity and recycle its ID.
		target := posMap.NewEntity(&Position{X: -1})
		w.RemoveEntity(target)
		posMap.NewEntity(&Position{X: float64(GetResource[Grid](w).Width)})
	}

	for range 5 {
		step()
	}

	// Restore and verify the entire state.
	expectNil(t, w.Restore(snapshot))

	expectEqual(t, 20, GetResource[Grid](w).Width)
	expectTrue(t, w.Alive(parent1))
	expectTrue(t, w.Alive(parent2))
	expectFalse(t, w.Alive(removed))
	expectTrue(t, w.Alive(recycled))

	query2 := NewFilter2[Position, Velocity](w).Query()
	expectEqual(t, 8, query2.Count())
	for query2.Next() {
		pos, vel := query2.Get()
		expectEqual(t, Velocity{X: 1, Y: 2}, *vel)
		expectEqual(t, float64(0), pos.Y)
	}
	query2.Close()

	query1 := NewFilter1[ChildOf](w).Query()
	expectEqual(t, 4, query1.Count())
	query1.Close()

	query1 = NewFilter1[ChildOf](w).Query(Rel[ChildOf](parent1))
	expectEqual(t, 2, query1.Count())
	query1.Close()

	query1 = NewFilter1[ChildOf](w).Query(Rel[ChildOf](parent2))
	expectEqual(t, 2, query1.Count())
	query1.Close()

	// Running the same simulation twice from the same snapshot must be deterministic.
	for range 5 {
		step()
	}
	state1 := snapshotState(w)

	expectNil(t, w.Restore(snapshot))
	for range 5 {
		step()
	}
	state2 := snapshotState(w)

	expectSlicesEqual(t, state1, state2)
}

func TestSnapshotSimulationDeterminism(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)
	velMap := NewMap[Velocity](w)
	for i := range 10 {
		e := posMap.NewEntity(&Position{X: float64(i)})
		velMap.Add(e, &Velocity{X: 1, Y: 2})
	}
	snapshot := w.Snapshot()

	simulate := func() []Position {
		for range 3 {
			query := NewFilter2[Position, Velocity](w).Query()
			for query.Next() {
				pos, vel := query.Get()
				pos.X += vel.X
				pos.Y += vel.Y
			}
			query.Close()
		}
		result := []Position{}
		query := NewFilter1[Position](w).Query()
		for query.Next() {
			result = append(result, *posMap.Get(query.Entity()))
		}
		query.Close()
		return result
	}

	want := simulate()
	expectNil(t, w.Restore(snapshot))
	got := simulate()
	expectSlicesEqual(t, want, got)

	// Restoring again must reproduce the exact same start state.
	expectNil(t, w.Restore(snapshot))
	startState := []Position{}
	query := NewFilter1[Position](w).Query()
	for query.Next() {
		startState = append(startState, *posMap.Get(query.Entity()))
	}
	query.Close()
	expectEqual(t, 10, len(startState))
	expectEqual(t, Position{X: 0}, startState[0])
	expectEqual(t, Position{X: 9}, startState[9])
}

// EntityRef is a component containing an entity reference.
type EntityRef struct {
	Target Entity
}

// cloneSnapshot creates a deep copy of a snapshot, for testing corrupted snapshots.
func cloneSnapshot(s *Snapshot) *Snapshot {
	clone := &Snapshot{
		version:   s.version,
		entities:  append([]Entity{}, s.entities...),
		next:      s.next,
		available: s.available,
		tables:    make([]snapshotTable, len(s.tables)),
		resources: make([]snapshotRes, len(s.resources)),
	}
	for i, table := range s.tables {
		clone.tables[i] = snapshotTable{
			entities:   append([]Entity{}, table.entities...),
			components: append([]reflect.Type{}, table.components...),
			columns:    make([]reflect.Value, len(table.columns)),
			relations:  append([]snapshotRel{}, table.relations...),
		}
		for j, column := range table.columns {
			copied := reflect.MakeSlice(column.Type(), column.Len(), column.Len())
			reflect.Copy(copied, column)
			clone.tables[i].columns[j] = copied
		}
	}
	copy(clone.resources, s.resources)
	return clone
}

// snapshotState returns a comparable representation of all entities of the world.
func snapshotState(w *World) []Entity {
	entities := []Entity{}
	query := NewUnsafeFilter(w).Query()
	for query.Next() {
		entities = append(entities, query.Entity())
	}
	query.Close()
	return entities
}

func TestSnapshotConvenienceFunctions(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)
	e := posMap.NewEntity(&Position{1, 2})

	snapshot := NewSnapshot(w)
	expectNotNil(t, snapshot)

	posMap.Get(e).X = 42
	expectNil(t, snapshot.Restore(w))
	expectEqual(t, Position{1, 2}, *posMap.Get(e))

	posMap.Get(e).X = 42
	w.MustRestore(snapshot)
	expectEqual(t, Position{1, 2}, *posMap.Get(e))

	// MustRestore panics on an invalid snapshot.
	expectPanicsWithValue(t, "invalid snapshot: snapshot is nil", func() {
		w.MustRestore(nil)
	})
}

func TestSnapshotUnregisteredResourceData(t *testing.T) {
	w := NewWorld(16)
	// Put a resource into the storage without registering its type.
	w.resources.resources[7] = &Position{1, 2}

	_, err := w.TakeSnapshot()
	expectTrue(t, errors.Is(err, ErrInvalidSnapshot))
}

func TestSnapshotValidateCorruptions(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)
	childMap := NewMap[ChildOf](w)
	parent := w.NewEntity()
	child := childMap.NewEntity(&ChildOf{}, parent)
	posMap.Add(child, &Position{1, 2})
	snapshot := w.Snapshot()

	// Incomplete entity pool.
	corrupt := cloneSnapshot(snapshot)
	corrupt.entities = corrupt.entities[:1]
	expectTrue(t, errors.Is(w.Restore(corrupt), ErrInvalidSnapshot))

	// Snapshot with an empty table.
	corrupt = cloneSnapshot(snapshot)
	corrupt.tables = append(corrupt.tables, snapshotTable{})
	expectTrue(t, errors.Is(w.Restore(corrupt), ErrInvalidSnapshot))

	// Component and column count mismatch.
	corrupt = cloneSnapshot(snapshot)
	corrupt.tables[0].components = append(corrupt.tables[0].components, reflect.TypeFor[Position]())
	expectTrue(t, errors.Is(w.Restore(corrupt), ErrInvalidSnapshot))

	// Duplicate entity across two tables.
	corrupt = cloneSnapshot(snapshot)
	dup := cloneSnapshot(snapshot).tables[0]
	corrupt.tables = append(corrupt.tables, dup)
	expectTrue(t, errors.Is(w.Restore(corrupt), ErrSnapshotEntity))

	// Relation target that is dead.
	corrupt = cloneSnapshot(snapshot)
	corrupt.entities[parent.id] = Entity{id: parent.id, gen: parent.Gen() + 1}
	expectTrue(t, errors.Is(w.Restore(corrupt), ErrSnapshotEntity))

	// Relation component that is not part of its table.
	corrupt = cloneSnapshot(snapshot)
	corrupt.tables[0].relations = append(corrupt.tables[0].relations, snapshotRel{
		component: reflect.TypeFor[ChildOf2](),
		target:    parent,
	})
	expectTrue(t, errors.Is(w.Restore(corrupt), ErrInvalidSnapshot))

	// The world must still be intact after all failed restores.
	expectTrue(t, w.Alive(parent))
	expectTrue(t, w.Alive(child))
	expectEqual(t, Position{1, 2}, *posMap.Get(child))
}

func TestSnapshotDetachedRelation(t *testing.T) {
	w := NewWorld(16)
	childMap := NewMap[ChildOf](w)

	parent := w.NewEntity()
	child := childMap.NewEntity(&ChildOf{}, parent)

	// Removing the target detaches the relation, i.e. sets the zero entity as target.
	w.RemoveEntity(parent)
	expectEqual(t, Entity{}, childMap.GetRelation(child))

	snapshot := w.Snapshot()

	// Re-create a parent and change the relation after the snapshot.
	parent = w.NewEntity()
	childMap.SetRelation(child, parent)

	expectNil(t, w.Restore(snapshot))

	expectEqual(t, Entity{}, childMap.GetRelation(child))

	// The re-created entity is not part of the snapshot, so it is not in the world.
	// Its ID and generation are recycled from the snapshot's entity pool.
	query := NewFilter1[ChildOf](w).Query()
	expectEqual(t, 1, query.Count())
	query.Close()

	next := w.NewEntity()
	expectEqual(t, parent, next)
}

func TestSnapshotUninitializedWorld(t *testing.T) {
	w := &World{}
	_, err := w.TakeSnapshot()
	expectTrue(t, errors.Is(err, ErrInvalidSnapshot))
}

func TestSnapshotTooManyComponents(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)
	e := posMap.NewEntity(&Position{1, 2})
	snapshot := w.Snapshot()

	// Fill the registry with fake entries, so that the component can't be registered.
	target := NewWorld(16)
	fillRegistry(&target.storage.registry.registry, maskTotalBits)
	expectTrue(t, errors.Is(target.Restore(snapshot), ErrInvalidSnapshot))

	// The world must be unchanged.
	expectFalse(t, target.Alive(e))
	expectEqual(t, 0, len(target.storage.components))
}

func TestSnapshotTooManyResources(t *testing.T) {
	source := NewWorld(16)
	AddResource(source, &Position{1, 2})
	snapshot := source.Snapshot()

	target := NewWorld(16)
	fillRegistry(&target.resources.registry, maskTotalBits)
	expectTrue(t, errors.Is(target.Restore(snapshot), ErrInvalidSnapshot))
	for i := range target.resources.resources {
		expectNil(t, target.resources.resources[i])
	}
}

// fillRegistry fills a registry with count fake, distinct types.
func fillRegistry(reg *registry, count int) {
	for i := range count {
		reg.registerComponent(reflect.ArrayOf(i+1, reflect.TypeFor[byte]()), maskTotalBits)
	}
}

func TestSnapshotDeadRelationTargetOnly(t *testing.T) {
	w := NewWorld(16)
	childMap := NewMap[ChildOf](w)
	parent := w.NewEntity()
	childMap.NewEntity(&ChildOf{}, parent)
	snapshot := w.Snapshot()

	// Remove the target's table entry, so it is not detected as a dead table entity,
	// and make it dead in the entity pool.
	corrupt := cloneSnapshot(snapshot)
	corrupt.entities[parent.id] = Entity{id: parent.id, gen: parent.Gen() + 1}
	tables := corrupt.tables[:0]
	for _, table := range corrupt.tables {
		if len(table.entities) == 1 && table.entities[0] == parent {
			continue
		}
		tables = append(tables, table)
	}
	corrupt.tables = tables

	expectTrue(t, errors.Is(w.Restore(corrupt), ErrSnapshotEntity))
}

func TestSnapshotNonTrivialComponents(t *testing.T) {
	w := NewWorld(16)
	mapA := NewMap[SliceComp](w)
	mapB := NewMap[PointerComp](w)

	a := mapA.NewEntity(&SliceComp{Slice: []int{1, 2, 3}})
	ptr := &PointerType{Pos: &Position{X: 1, Y: 2}}
	b := mapB.NewEntity(&PointerComp{Ptr: ptr, Value: 42})

	snapshot := w.Snapshot()

	mapA.Get(a).Slice[0] = 100
	mapB.Get(b).Value = 0
	mapB.Get(b).Ptr = nil

	expectNil(t, w.Restore(snapshot))

	// Values are restored, but pointer-like fields are copied shallowly.
	expectSlicesEqual(t, []int{100, 2, 3}, mapA.Get(a).Slice)
	expectEqual(t, 42, mapB.Get(b).Value)
	expectTrue(t, mapB.Get(b).Ptr == ptr)
	expectEqual(t, Position{X: 1, Y: 2}, *mapB.Get(b).Ptr.Pos)
}

func TestSnapshotTakenWhileLocked(t *testing.T) {
	w := NewWorld(16)
	posMap := NewMap[Position](w)
	e := posMap.NewEntity(&Position{1, 2})

	query := NewFilter1[Position](w).Query()
	expectTrue(t, query.Next())

	// Taking a snapshot does not modify the world, so it works while locked.
	snapshot := w.Snapshot()
	query.Close()

	posMap.Get(e).X = 42
	expectNil(t, w.Restore(snapshot))
	expectEqual(t, Position{1, 2}, *posMap.Get(e))
}
