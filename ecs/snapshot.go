package ecs

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"unsafe"
)

// snapshotVersion is the version of the [Snapshot] layout.
// It is used to reject snapshots created by an incompatible version.
const snapshotVersion = 1

// Errors returned by [World.Restore], [Snapshot.Restore],
// [World.TakeSnapshot] and related functions.
var (
	// ErrInvalidSnapshot indicates a nil, uninitialized or corrupted [Snapshot].
	ErrInvalidSnapshot = errors.New("invalid snapshot")
	// ErrSnapshotLocked indicates that a snapshot was restored into a locked world.
	// Finish or close all open queries before restoring.
	ErrSnapshotLocked = errors.New("cannot restore snapshot into a locked world")
	// ErrSnapshotResource indicates that a resource cannot be copied into a snapshot.
	ErrSnapshotResource = errors.New("resource cannot be snapshotted")
	// ErrSnapshotEntity indicates that a snapshot contains an invalid entity handle,
	// like a removed entity or a relation target that is not alive.
	ErrSnapshotEntity = errors.New("invalid entity in snapshot")
)

// Snapshot is an in-memory snapshot of a [World]'s state.
//
// A snapshot contains all entities, all component values and all resources of the world.
// It is independent of the world it was created from: later changes to the world,
// or to the snapshot through [World.Restore], do not affect an existing snapshot.
//
// Entities keep their IDs and generations, so that entities stored in components,
// as relation targets or elsewhere still refer to the same entities after a restore.
//
// Like [World.CopyEntity], component values are copied shallowly.
// Pointer-like fields of components (e.g. pointers, slices and maps) still point
// to the same data after a restore. Resources are copied into new pointers.
//
// # Usage
//
// Take a snapshot with [World.Snapshot], and restore it with [World.Restore]:
//
//	snapshot := world.Snapshot()
//	// ... simulate ...
//	world.Restore(snapshot)
//
// Multiple snapshots can be taken and restored in any order.
//
// Snapshots should be created and restored on the same goroutine that uses the world.
// Ark's worlds are not safe for concurrent use.
type Snapshot struct {
	entities  []Entity        // Entity pool state, including generations and the free list
	tables    []snapshotTable // Component data, grouped by table
	resources []snapshotRes   // Resource values
	next      entityID        // Next free entity in the pool
	available uint32          // Number of recycled entities in the pool
	version   uint32          // Snapshot format version
}

// snapshotTable stores entities and component values of a single table.
type snapshotTable struct {
	entities   []Entity        // Entities in table row order
	components []reflect.Type  // Component type per column
	columns    []reflect.Value // Component values, as typed slices
	relations  []snapshotRel   // Relation targets of the table
}

// snapshotRel stores a relation target, identified by component type.
type snapshotRel struct {
	component reflect.Type
	target    Entity
}

// snapshotRes stores a copy of a resource.
type snapshotRes struct {
	value reflect.Value // Pointer to the copied resource value
	typ   reflect.Type  // Resource element type
}

// Snapshot creates a new [Snapshot] of the world.
//
// The snapshot is independent of the world. Entities keep their IDs and generations,
// components keep their values, and relation targets and entity references stay valid.
//
// If a resource cannot be copied (e.g. it is not a pointer, or the pointer is nil),
// Snapshot panics. Use [World.TakeSnapshot] to get an error instead.
//
// Snapshot is a read-only operation and may also be called while the world is locked
// by a query. It is not safe to call it concurrently with world modifications.
func (w *World) Snapshot() *Snapshot {
	snapshot, err := w.TakeSnapshot()
	if err != nil {
		panic(err.Error())
	}
	return snapshot
}

// TakeSnapshot creates a new [Snapshot] of the world, returning an error instead of panicking.
//
// See [World.Snapshot] for details.
func (w *World) TakeSnapshot() (*Snapshot, error) {
	s := &w.storage

	if len(s.entityPool.entities) < reservedEntities {
		return nil, fmt.Errorf("%w: entity pool is not initialized", ErrInvalidSnapshot)
	}

	snapshot := &Snapshot{
		version:   snapshotVersion,
		entities:  append([]Entity{}, s.entityPool.entities...),
		next:      s.entityPool.next,
		available: s.entityPool.available,
	}

	for i := range s.archetypes {
		archetype := &s.archetypes[i]
		for _, tableID := range archetype.tables.tables {
			table := &s.tables[tableID]
			rows := table.Len()
			if rows == 0 {
				continue
			}
			snapshot.tables = append(snapshot.tables, snapshotTableData(table, s, rows))
		}
	}

	for i := range w.resources.resources {
		res := w.resources.resources[i]
		if res == nil {
			continue
		}
		tp, ok := w.resources.registry.ComponentType(uint8(i))
		if !ok {
			return nil, fmt.Errorf("%w: resource with ID %d is not registered", ErrInvalidSnapshot, i)
		}
		value, err := copyResourceToSnapshot(res, tp)
		if err != nil {
			return nil, err
		}
		snapshot.resources = append(snapshot.resources, snapshotRes{typ: tp, value: value})
	}

	return snapshot, nil
}

// snapshotTableData copies the data of a single table into a [snapshotTable].
func snapshotTableData(table *table, s *storage, rows int) snapshotTable {
	result := snapshotTable{
		entities:   make([]Entity, rows),
		components: make([]reflect.Type, len(table.columns)),
		columns:    make([]reflect.Value, len(table.columns)),
	}

	for i := range rows {
		result.entities[i] = table.GetEntity(uintptr(i))
	}

	for i := range table.columns {
		column := &table.columns[i]
		tp, _ := s.registry.ComponentType(table.ids[i].id)
		data := reflect.MakeSlice(reflect.SliceOf(tp), rows, rows)
		reflect.Copy(data, column.data.Slice(0, rows))

		result.components[i] = tp
		result.columns[i] = data

		if column.isRelation {
			result.relations = append(result.relations, snapshotRel{
				component: tp,
				target:    column.target,
			})
		}
	}

	return result
}

// copyResourceToSnapshot copies a resource into a new, independent pointer.
// The resource must be a non-nil pointer to a value of the registered type.
func copyResourceToSnapshot(res any, registered reflect.Type) (value reflect.Value, err error) {
	rv := reflect.ValueOf(res)
	if rv.Kind() != reflect.Pointer {
		return value, fmt.Errorf("%w: resource of type %s is not a pointer",
			ErrSnapshotResource, rv.Type())
	}
	if rv.IsNil() {
		return value, fmt.Errorf("%w: resource of type %s is a nil pointer",
			ErrSnapshotResource, rv.Type())
	}
	if rv.Type().Elem() != registered {
		return value, fmt.Errorf("%w: resource is a %s, but %s is registered",
			ErrSnapshotResource, rv.Type(), registered)
	}

	copied := reflect.New(rv.Type().Elem())
	copied.Elem().Set(rv.Elem())
	return copied, nil
}

// Restore restores the world from a [Snapshot].
//
// Restore replaces all entities, component values and resources of the world
// with the state stored in the snapshot. Entities keep their IDs and generations,
// so entity references and relation targets remain valid.
//
// Existing filters, queries (created after the restore), mappers and observers
// continue to work after a restore. The restore itself does not emit observer events.
//
// Restoring is only possible while the world is not locked.
// If there are open queries, Restore returns an error wrapping [ErrSnapshotLocked].
//
// Components and resources present in the snapshot but not registered in the world
// are registered automatically, provided the registry is not full.
//
// Entity handles that were valid before the restore but do not exist in the snapshot
// are reported as dead by [World.Alive] afterwards.
// Entity handles in the snapshot that refer to removed entities are rejected.
func (w *World) Restore(snapshot *Snapshot) error {
	if snapshot == nil {
		return fmt.Errorf("%w: snapshot is nil", ErrInvalidSnapshot)
	}
	if snapshot.version != snapshotVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidSnapshot, snapshot.version)
	}
	if w.IsLocked() {
		return fmt.Errorf("%w: finish or close all open queries first", ErrSnapshotLocked)
	}

	// Validate the snapshot before modifying the world.
	componentTypes, err := snapshot.validate()
	if err != nil {
		return err
	}

	// Prepare independent copies of all resources before changing any data.
	resources := make([]preparedResource, 0, len(snapshot.resources))
	for i := range snapshot.resources {
		resources = append(resources, preparedResource{
			typ:   snapshot.resources[i].typ,
			value: copyResourceFromSnapshot(&snapshot.resources[i]),
		})
	}

	// Register all required component types before changing any data.
	registry := &w.storage.registry
	newComponents := 0
	for tp := range componentTypes {
		if _, ok := registry.Components[tp]; !ok {
			newComponents++
		}
	}
	if len(registry.Components)+newComponents > maskTotalBits {
		return fmt.Errorf("%w: too many component types, maximum is %d",
			ErrInvalidSnapshot, maskTotalBits)
	}
	ids := make(map[reflect.Type]ID, len(componentTypes))
	for _, tp := range sortedTypes(componentTypes) {
		ids[tp] = w.componentID(tp)
	}

	// Register all required resource types before changing any data.
	newResources := 0
	for i := range snapshot.resources {
		tp := snapshot.resources[i].typ
		if _, ok := w.resources.registry.Components[tp]; !ok {
			newResources++
		}
	}
	if len(w.resources.registry.Components)+newResources > maskTotalBits {
		return fmt.Errorf("%w: too many resource types, maximum is %d",
			ErrInvalidSnapshot, maskTotalBits)
	}
	for i := range resources {
		resources[i].id = w.resourceID(resources[i].typ)
	}

	// From here on, the world is modified.
	lock := w.lock()
	defer w.unlock(lock)

	s := &w.storage

	// Cache entries of relation tables must be updated before freeing the tables.
	for i := range s.archetypes {
		archetype := &s.archetypes[i]
		if !archetype.HasRelations() {
			continue
		}
		for _, tableID := range archetype.tables.tables {
			s.cache.removeTable(&s.tables[tableID])
		}
	}

	// Restore the entity pool exactly, including generations and the free list.
	entities := append([]Entity{}, snapshot.entities...)
	s.entityPool = entityPool{
		entities:  entities,
		next:      snapshot.next,
		available: snapshot.available,
		reserved:  reservedEntities,
	}
	s.entityPool.pointer = unsafe.Pointer(&entities[0])

	s.entities = make([]entityIndex, len(entities))
	for i := range s.entities {
		s.entities[i] = entityIndex{table: maxTableID}
	}
	s.isTarget = make([]bool, len(entities))

	// Reset all archetypes. Must happen after replacing the entity pool,
	// as resetting tables clears entity references.
	for i := range s.archetypes {
		s.archetypes[i].Reset(s)
	}

	// Restore entities and component values.
	for i := range snapshot.tables {
		tableSnapshot := &snapshot.tables[i]
		w.restoreSnapshotTable(tableSnapshot, ids)
	}

	// Restore resources.
	for i := range w.resources.resources {
		w.resources.resources[i] = nil
	}
	for _, res := range resources {
		w.resources.resources[res.id.id] = res.value
	}

	return nil
}

// sortedTypes returns the types of a set in a deterministic order.
// Component registration order determines component IDs,
// which should not depend on map iteration order.
func sortedTypes(types map[reflect.Type]struct{}) []reflect.Type {
	result := make([]reflect.Type, 0, len(types))
	for tp := range types {
		result = append(result, tp)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].String() < result[j].String()
	})
	return result
}

// preparedResource is a resource copied from a snapshot, ready to be added to a world.
type preparedResource struct {
	value any
	typ   reflect.Type
	id    ResID
}

// restoreSnapshotTable restores entities and component values of a single snapshot table.
//
// All component types and relation targets are validated by [Snapshot.validate].
func (w *World) restoreSnapshotTable(tableSnapshot *snapshotTable, ids map[reflect.Type]ID) {
	s := &w.storage

	var mask bitMask
	componentIDs := make([]ID, len(tableSnapshot.components))
	for i, tp := range tableSnapshot.components {
		componentIDs[i] = ids[tp]
	}

	node := s.graph.FindAdd(0, componentIDs, &mask)

	var archetype *archetype
	if archetypeID, ok := node.GetArchetype(); ok {
		archetype = &s.archetypes[archetypeID]
	} else {
		archetype = s.createArchetype(node)
	}

	relations := make([]relationID, 0, len(tableSnapshot.relations))
	for _, rel := range tableSnapshot.relations {
		relations = append(relations, relationID{component: ids[rel.component], target: rel.target})
	}

	table, ok := archetype.GetTable(s, relations)
	if !ok {
		table = s.createTable(archetype, relations)
	}

	rows := len(tableSnapshot.entities)
	start := table.Len()
	table.Alloc(uint32(rows))

	for i := range rows {
		entity := tableSnapshot.entities[i]
		row := uint32(start + i)
		table.SetEntity(row, entity)
		s.entities[entity.id] = entityIndex{table: table.id, row: row}
	}

	for i, tp := range tableSnapshot.components {
		column := table.Column(ids[tp])
		reflect.Copy(column.data.Slice(start, start+rows), tableSnapshot.columns[i])
	}

	for _, rel := range tableSnapshot.relations {
		if rel.target.IsZero() {
			continue
		}
		s.isTarget[rel.target.id] = true
	}
}

// validate checks the snapshot's internal consistency and
// returns all component types that occur in it.
func (s *Snapshot) validate() (map[reflect.Type]struct{}, error) {
	if len(s.entities) < reservedEntities {
		return nil, fmt.Errorf("%w: entity pool is incomplete", ErrInvalidSnapshot)
	}

	componentTypes := map[reflect.Type]struct{}{}
	seen := make([]bool, len(s.entities))

	for i := range s.tables {
		table := &s.tables[i]
		rows := len(table.entities)
		if rows == 0 {
			return nil, fmt.Errorf("%w: snapshot contains an empty table", ErrInvalidSnapshot)
		}
		if len(table.components) != len(table.columns) {
			return nil, fmt.Errorf("%w: component and column count mismatch", ErrInvalidSnapshot)
		}

		for j := range table.entities {
			entity := table.entities[j]
			if int(entity.id) < reservedEntities || !s.alive(entity) {
				return nil, fmt.Errorf("%w: table contains dead entity %v",
					ErrSnapshotEntity, entity)
			}
			if seen[entity.id] {
				return nil, fmt.Errorf("%w: duplicate entity %v", ErrSnapshotEntity, entity)
			}
			seen[entity.id] = true
		}

		for j, tp := range table.components {
			column := table.columns[j]
			if !column.IsValid() || column.Kind() != reflect.Slice ||
				column.Type().Elem() != tp || column.Len() != rows {
				return nil, fmt.Errorf("%w: invalid column for component type %s",
					ErrInvalidSnapshot, tp)
			}
			componentTypes[tp] = struct{}{}
		}

		for _, rel := range table.relations {
			if !rel.target.IsZero() && !s.alive(rel.target) {
				return nil, fmt.Errorf("%w: relation component %s targets dead entity %v",
					ErrSnapshotEntity, rel.component, rel.target)
			}
			if _, ok := componentTypes[rel.component]; !ok {
				return nil, fmt.Errorf("%w: relation component type %s is not part of its table",
					ErrInvalidSnapshot, rel.component)
			}
		}
	}

	for i := range s.resources {
		res := &s.resources[i]
		if !res.value.IsValid() || res.value.Kind() != reflect.Pointer ||
			res.value.IsNil() || res.value.Type().Elem() != res.typ {
			return nil, fmt.Errorf("%w: invalid resource of type %s",
				ErrInvalidSnapshot, res.typ)
		}
	}

	return componentTypes, nil
}

// alive returns whether an entity is alive in the snapshot's entity pool.
//
// Mirrors [entityPool.Alive]: entity IDs are stored as a free-list link
// for recycled entity slots, so only the generation determines aliveness.
func (s *Snapshot) alive(entity Entity) bool {
	if int(entity.id) >= len(s.entities) || int(entity.id) < reservedEntities {
		return false
	}
	return s.entities[entity.id].gen == entity.gen
}

// copyResourceFromSnapshot copies a snapshot resource into a new, independent pointer.
func copyResourceFromSnapshot(res *snapshotRes) any {
	copied := reflect.New(res.typ)
	copied.Elem().Set(res.value.Elem())
	return copied.Interface()
}

// MustRestore restores the world from a [Snapshot], and panics on error.
//
// See [World.Restore] for details.
func (w *World) MustRestore(snapshot *Snapshot) {
	if err := w.Restore(snapshot); err != nil {
		panic(err.Error())
	}
}

// Restore restores the given world from this snapshot.
//
// It is a convenience wrapper around [World.Restore].
func (s *Snapshot) Restore(w *World) error {
	if w == nil {
		return fmt.Errorf("%w: world is nil", ErrInvalidSnapshot)
	}
	return w.Restore(s)
}

// NewSnapshot creates a new [Snapshot] of the given world.
//
// It is a convenience wrapper around [World.Snapshot].
func NewSnapshot(w *World) *Snapshot {
	return w.Snapshot()
}
