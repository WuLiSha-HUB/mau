import { _decorator, Component } from 'cc';

const { ccclass, property } = _decorator;

/** 剧情节点——无状态树的一节：正片 / 分支反馈 / 结局承载 */
@ccclass('StoryNode')
export class StoryNode extends Component
{
    /** 节点键 */
    @property
    public key: string = '';

    /** 视频资源键 */
    @property
    public videoKey: string = '';

    /** 出口键列表（有序） */
    @property({ type: [String] })
    public exits: string[] = [];

    /** 是否为结局承载 */
    @property
    public isEnding: boolean = false;

    /**
     * 出口数量
     * @returns 出口数
     */
    public ExitCount(): number
    {
        return this.exits.length;
    }

    /**
     * 按索引取出口键
     * @param index 出口序号
     * @returns 出口键；越界返回空串
     */
    public ExitAt(index: number): string
    {
        if (index < 0 || index >= this.exits.length)
        {
            return '';
        }
        return this.exits[index];
    }
}
