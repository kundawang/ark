+++
title = 'World snapshots'
type = "docs"
weight = 85
description = "Saving and restoring world state."
+++

A snapshot captures the entire state of a world in memory:
all entities, all component values, and all resources.
Snapshots are useful for checkpoints, replaying simulations, and debugging.
For example, a replay can be started from a checkpoint at a certain frame instead of from the beginning.

## Taking and restoring snapshots

{{< api ecs World.Snapshot >}} creates a snapshot of the world.
{{< api ecs World.Restore >}} brings the world back to the state of a snapshot:

{{< code-func snapshots_test.go TestSnapshotBasics >}}

Snapshots are independent of the world they were created from.
Changes to the world after taking a snapshot do not affect the snapshot,
and restoring a snapshot multiple times always results in the same state.

A snapshot can be created while the world is locked by a query.
Restoring is only possible while the world is not locked;
{{< api ecs World.Restore >}} returns an error wrapping {{< api ecs ErrSnapshotLocked >}} otherwise.

The convenience method {{< api ecs World.MustRestore >}} panics instead of returning an error.

## Multiple checkpoints

Any number of snapshots can be taken and restored in any order,
for example to implement rolling checkpoints in a simulation:

{{< code-func snapshots_test.go TestSnapshotCheckpoints >}}

## Entities and entity references

Entities keep their ID and generation across a restore.
Thus, entities stored in components, in resources, or as relation targets
still refer to the same entities after restoring a snapshot:

{{< code-func snapshots_test.go TestSnapshotRelations >}}

Entity handles that were valid before a restore, but do not exist in the snapshot,
are reported as dead by {{< api ecs World.Alive >}} afterwards.
After restoring, the entity pool continues from the state stored in the snapshot,
so recycled entity IDs and their generations behave exactly as before.

## Resources

Resources are part of a snapshot.
Restoring creates new resource values, so subsequent changes to a world resource
do not affect the snapshot.
A resource that cannot be copied (i.e. not a pointer, or a nil pointer)
causes {{< api ecs World.Snapshot >}} to panic,
and {{< api ecs World.TakeSnapshot >}} to return an error wrapping {{< api ecs ErrSnapshotResource >}}:

{{< code-func snapshots_test.go TestSnapshotResources >}}

## Components and archetypes

Snapshots store component values grouped by archetype table.
After a restore, the world has the same archetypes and tables (including relation tables) as before,
and existing filters, mappers and observers continue to work.
The restore itself does not emit any observer events.

Component values are copied shallowly, like in {{< api ecs World.CopyEntity >}}.
Pointer-like component fields (e.g. pointers, slices and maps) still point to the same data after a restore.

Components present in a snapshot but not registered in the target world are registered automatically.
Invalid snapshots, for example snapshots with duplicate entities or dead relation targets,
are rejected with an error wrapping {{< api ecs ErrSnapshotEntity >}},
and the world is left unchanged.

## Limitations

- Snapshots are in-memory only. For persistence, see [ark-serde](https://github.com/mlange-42/ark-serde).
- Snapshots are not safe for concurrent use with world modifications.
- Restoring does not reset the filter cache or unregister observers.
