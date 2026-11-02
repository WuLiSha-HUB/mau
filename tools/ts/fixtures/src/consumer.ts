/** 样例消费方——走 paths 别名引用 model */

import { Entity, EntityKind, EntityStore, makeEntity } from '@/model';

/**
 * 建一个预填仓库
 * @returns 仓库
 */
export function buildStore(): EntityStore
{
    const store = new EntityStore();
    const first: Entity = makeEntity('a');
    store.add(first);
    store.add({ id: 'b', kind: EntityKind.Npc, name: 'npc-b' });
    return store;
}

/**
 * 读取名字
 * @param store 仓库
 * @param id 标识
 * @returns 名字；缺省空串
 */
export function nameOf(store: EntityStore, id: string): string
{
    const hit = store.get(id);
    if (hit === undefined)
    {
        return '';
    }
    return hit.name;
}
