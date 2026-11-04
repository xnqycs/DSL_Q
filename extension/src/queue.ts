// A download can gain its filename/response while an earlier check is awaiting
// Chrome. Coalesce events, but always run again after the current check finishes.
export function downloadQueue(work: (id: number) => Promise<void>) {
  const running = new Map<number, Promise<void>>();
  const dirty = new Set<number>();
  return (id: number): Promise<void> => {
    dirty.add(id);
    const existing = running.get(id);
    if (existing) return existing;
    const task = Promise.resolve().then(async () => {
      while (dirty.delete(id)) await work(id);
    }).finally(() => { running.delete(id); dirty.delete(id); });
    running.set(id, task);
    return task;
  };
}
