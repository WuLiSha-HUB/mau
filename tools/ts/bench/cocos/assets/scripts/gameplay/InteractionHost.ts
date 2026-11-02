import { _decorator, Component, Node, instantiate, Prefab } from 'cc';

const { ccclass, property } = _decorator;

/** 玩法容器——按节点需求挂载轻量互动玩法（IAA 变现） */
@ccclass('InteractionHost')
export class InteractionHost extends Component
{
    /** 玩法预制体表 */
    @property({ type: [Prefab] })
    public prefabs: Prefab[] = [];

    /** 承载槽位 */
    @property(Node)
    public slot: Node | null = null;

    /** 当前玩法实例 */
    private active: Node | null = null;

    /**
     * 启动玩法
     * @param prefabIndex 预制体序号
     * @returns 是否启动成功
     */
    public StartGameplay(prefabIndex: number): boolean
    {
        if (this.slot === null)
        {
            return false;
        }
        if (prefabIndex < 0 || prefabIndex >= this.prefabs.length)
        {
            return false;
        }
        this.StopGameplay();
        this.active = instantiate(this.prefabs[prefabIndex]);
        this.slot.addChild(this.active);
        return true;
    }

    /** 停止当前玩法 */
    public StopGameplay(): void
    {
        if (this.active === null)
        {
            return;
        }
        this.active.removeFromParent();
        this.active.destroy();
        this.active = null;
    }

    /**
     * 当前玩法名
     * @returns 玩法名；无实例返回空串
     */
    public ActiveName(): string
    {
        if (this.active === null)
        {
            return '';
        }
        return this.active.name;
    }
}
