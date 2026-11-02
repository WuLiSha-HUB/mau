import { _decorator, Component, EventTarget } from 'cc';
import { StoryGraph } from './StoryGraph';
import { StoryNode } from './StoryNode';

const { ccclass, property } = _decorator;

/** 剧情运行时——当前节点指针 + 事件广播 */
@ccclass('StoryRuntime')
export class StoryRuntime extends Component
{
    /** 剧情图 */
    @property(StoryGraph)
    public graph: StoryGraph | null = null;

    /** 当前节点键 */
    private currentKey: string = '';

    /** 事件面 */
    public events: EventTarget = new EventTarget();

    /** 加载状态 */
    private loading: boolean = false;

    /**
     * 载入剧情包
     * @param packKey 包键
     * @param onDone 完成回调
     */
    public Load(packKey: string, onDone: () => void): void
    {
        if (this.loading)
        {
            return;
        }
        this.loading = true;
        if (this.graph !== null)
        {
            this.graph.Build();
            const entry = this.graph.Entry();
            if (entry !== null)
            {
                this.currentKey = entry.key;
            }
        }
        this.loading = false;
        this.events.emit('loaded', packKey);
        onDone();
    }

    /**
     * 推进到指定节点
     * @param key 目标节点键
     * @returns 是否推进成功
     */
    public GoTo(key: string): boolean
    {
        if (this.graph === null)
        {
            return false;
        }
        const node = this.graph.Get(key);
        if (node === null)
        {
            return false;
        }
        this.currentKey = key;
        this.events.emit('enter', key);
        return true;
    }

    /**
     * 当前节点
     * @returns 节点；未进入返回 null
     */
    public Current(): StoryNode | null
    {
        if (this.graph === null || this.currentKey.length === 0)
        {
            return null;
        }
        return this.graph.Get(this.currentKey);
    }
}
