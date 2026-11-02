import { _decorator, Component } from 'cc';
import { StoryNode } from './StoryNode';

const { ccclass, property } = _decorator;

/** 剧情图——节点表 + 起点定位（无状态分支树） */
@ccclass('StoryGraph')
export class StoryGraph extends Component
{
    /** 全部节点 */
    @property({ type: [StoryNode] })
    public nodes: StoryNode[] = [];

    /** 起点节点键 */
    @property
    public entryKey: string = '';

    /** 键 → 节点 索引 */
    private index: Map<string, StoryNode> = new Map<string, StoryNode>();

    /** 装配索引 */
    public Build(): void
    {
        this.index.clear();
        for (let i = 0; i < this.nodes.length; i = i + 1)
        {
            const node = this.nodes[i];
            this.index.set(node.key, node);
        }
    }

    /**
     * 取节点
     * @param key 节点键
     * @returns 节点；不存在返回 null
     */
    public Get(key: string): StoryNode | null
    {
        const found = this.index.get(key);
        if (found === undefined)
        {
            return null;
        }
        return found;
    }

    /**
     * 取起点节点
     * @returns 起点节点；不存在返回 null
     */
    public Entry(): StoryNode | null
    {
        return this.Get(this.entryKey);
    }
}
