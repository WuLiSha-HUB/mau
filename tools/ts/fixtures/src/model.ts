/** 样例模型——模块文件（顶层有 export） */

/** 实体种类 */
export enum EntityKind
{
    Player = 'player',
    Npc = 'npc'
}

/** 实体契约 */
export interface Entity
{
    /** 唯一标识 */
    id: string;
    /** 种类 */
    kind: EntityKind;
    /** 显示名 */
    name: string;
}

/** 标识简写 */
export type EntityId = string;

/** 实体仓库 */
export class EntityStore
{
    /** 内部表 */
    private readonly items = new Map<string, Entity>();

    /**
     * 加入实体
     * @param entity 实体
     */
    public add(entity: Entity): void
    {
        this.items.set(entity.id, entity);
    }

    /**
     * 按 id 取实体
     * @param id 标识
     * @returns 实体；不存在 undefined
     */
    public get(id: EntityId): Entity | undefined
    {
        return this.items.get(id);
    }

    /** 数量 */
    public get count(): number
    {
        return this.items.size;
    }
}

/**
 * 造一个最小实体
 * @param id 标识
 * @returns 实体
 */
export function makeEntity(id: EntityId): Entity
{
    return { id: id, kind: EntityKind.Player, name: 'entity-' + id };
}
