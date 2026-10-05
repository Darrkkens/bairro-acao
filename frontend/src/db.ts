// One IndexedDB database for everything the phone keeps until the server has it:
// the outbox (walks, photos, finish) and the GPS track of the walk in progress.
let opened: Promise<IDBDatabase> | null = null

function db(): Promise<IDBDatabase> {
  opened ??= new Promise((resolve, reject) => {
    const req = indexedDB.open('bairro-em-acao', 2)
    req.onupgradeneeded = () => {
      const d = req.result
      if (!d.objectStoreNames.contains('outbox')) d.createObjectStore('outbox', { keyPath: 'seq', autoIncrement: true })
      if (!d.objectStoreNames.contains('track')) d.createObjectStore('track', { keyPath: 'seq', autoIncrement: true }).createIndex('walkId', 'walkId')
    }
    req.onsuccess = () => {
      // Let a newer version of the app (another tab) upgrade the schema.
      req.result.onversionchange = () => {
        req.result.close()
        opened = null
      }
      resolve(req.result)
    }
    req.onerror = () => {
      opened = null
      reject(req.error)
    }
  })
  return opened
}

/** Runs one transaction; resolves with the request's result once it commits. */
export async function withStore<T>(name: 'outbox' | 'track', mode: IDBTransactionMode, run: (store: IDBObjectStore) => IDBRequest<T> | void): Promise<T> {
  const database = await db()
  return new Promise((resolve, reject) => {
    const t = database.transaction(name, mode)
    const req = run(t.objectStore(name))
    t.oncomplete = () => resolve(req ? req.result : (undefined as T))
    t.onerror = () => reject(t.error)
    t.onabort = () => reject(t.error)
  })
}
