import { concat, from, Observable, of } from 'rxjs'
import { filter, map } from 'rxjs/operators'

import { getPlatformName } from '../../shared/util/context'

import { fromBrowserEvent } from './fromBrowserEvent'
import { LocalStorageItems, SyncStorageItems, ManagedStorageItems } from './types'

interface ExtensionStorageItems {
    local: LocalStorageItems
    sync: SyncStorageItems
    managed: ManagedStorageItems
}

/**
 * Type-safe access to browser extension storage.
 *
 * `undefined` in non-browser context (in-page integration). Make sure to check `isInPage`/`isExtension` first.
 */
export const storage: {
    [K in browser.storage.AreaName]: browser.storage.StorageArea<ExtensionStorageItems[K]>
} & {
    onChanged: browser.CallbackEventEmitter<
        (
            changes: browser.storage.ChangeDict<ExtensionStorageItems[typeof areaName]>,
            areaName: browser.storage.AreaName
        ) => void
    >
} = globalThis.browser && browser.storage

export const observeStorageKey = <A extends browser.storage.AreaName, K extends keyof ExtensionStorageItems[A]>(
    areaName: A,
    key: K
): Observable<ExtensionStorageItems[A][K] | undefined> => {
    if (getPlatformName() !== 'chrome-extension' && areaName === 'managed') {
        // Accessing managed storage throws an error on Firefox and on Safari.
        return of(undefined)
    }
    return concat(
        // Start with current value of the item
        from((storage[areaName] as browser.storage.StorageArea<ExtensionStorageItems[A]>).get(key)).pipe(
            map(items => (items as ExtensionStorageItems[A])[key])
        ),
        // Emit every new value from change events that affect that item
        fromBrowserEvent(storage.onChanged).pipe(
            filter(([, name]) => areaName === name),
            map(([changes]) => changes),
            filter((changes): changes is {
                [k in K]: browser.storage.StorageChange<ExtensionStorageItems[A][K]>
            } => Object.prototype.hasOwnProperty.call(changes, key)),
            map(changes => changes[key].newValue)
        )
    )
}

export async function getStorageKey<A extends browser.storage.AreaName, K extends keyof ExtensionStorageItems[A]>(
    areaName: A,
    key: K
): Promise<ExtensionStorageItems[A][K] | undefined> {
    if (!storage?.[areaName]) {
        return Promise.resolve(undefined)
    }
    const result = await (storage[areaName] as browser.storage.StorageArea<ExtensionStorageItems[A]>).get(key)
    return result[key]
}

export function setStorageKey<
    A extends browser.storage.AreaName,
    K extends keyof ExtensionStorageItems[A],
    V extends ExtensionStorageItems[A][K]
>(areaName: A, key: K, value: V): Promise<void> {
    if (!storage?.[areaName]) {
        return Promise.resolve()
    }
    return storage[areaName].set({ [key]: value })
}
