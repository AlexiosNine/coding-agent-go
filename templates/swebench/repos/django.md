Django repository notes:
- Deletion behavior usually lives in `django/db/models/deletion.py`, especially `Collector.delete()`.
- Distinguish the single-instance fast-delete early-return branch from the later `self.fast_deletes` queryset loop. If the issue says deleting one model instance with no dependencies does not clear its primary key, inspect the early-return branch guarded by `self.can_fast_delete(instance)`.
- The expected fix should preserve the existing delete count behavior and clear the deleted instance primary key before returning from that early branch.
- Do not add broad primary-key clearing to unrelated queryset fast-delete loops unless the issue specifically asks for queryset behavior.
