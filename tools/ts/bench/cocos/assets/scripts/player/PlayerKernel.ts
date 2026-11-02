import { _decorator, Component, Node } from 'cc';
import { StoryRuntime } from '../story/StoryRuntime';
import { InteractionHost } from '../gameplay/InteractionHost';

const { ccclass, property } = _decorator;

/** 播放内核——视频驱动 + 剧情推进 + 玩法挂载三件事的装配点 */
@ccclass('PlayerKernel')
export class PlayerKernel extends Component
{
    /** 剧情运行时 */
    @property(StoryRuntime)
    public story: StoryRuntime | null = null;

    /** 玩法容器 */
    @property(InteractionHost)
    public host: InteractionHost | null = null;

    /** 是否已就绪 */
    private ready: boolean = false;

    /**
     * 预装配——资源与剧情图就绪后回调
     * @param onReady 就绪回调
     */
    public Prepare(onReady: () => void): void
    {
        if (this.ready)
        {
            onReady();
            return;
        }
        this.ready = true;
        if (this.story !== null)
        {
            this.story.Load('ep01_s01', () => { onReady(); });
            return;
        }
        onReady();
    }

    /**
     * 挂载玩法容器到节点
     * @param target 目标节点
     */
    public AttachGameplay(target: Node): void
    {
        if (this.host === null)
        {
            return;
        }
        target.addChild(this.host.node);
    }
}
