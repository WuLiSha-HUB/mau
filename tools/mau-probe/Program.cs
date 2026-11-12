using System.Reflection;
using System.Text;
using Mau.Runtime;

namespace MauProbe;

/// <summary>
/// 乙1 对拍器可行性探针——独立进程驱动 C# 生成物，导出 trace 序列。
/// 只读引用 mauout 的 dll 副本，不触碰 mau / CH4 仓库。
/// </summary>
internal static class Program
{
    private static int Main(string[] args)
    {
        Console.OutputEncoding = Encoding.UTF8;
        int frames = args.Length > 0 && int.TryParse(args[0], out int n) ? n : 20;
        string flowDll = Path.Combine(AppContext.BaseDirectory, "FL_TextCat.dll");

        AuditStore.Default = new AuditStore();
        DataBox.Bind<FileSystemService>(new FileSystemService(
            new string[] { "D:\\Mau\\WorkSpace" },
            "D:\\Mau\\WorkSpace\\tools\\CatTemp\\fs_recycle"));

        ThreadGuard guard = new ThreadGuard();
        OA oa = new OA(guard);
        DataBox.Bind<IOA>(oa);
        oa.Audit = AuditStore.Default;
        CommandBus cmd = new CommandBus(guard);
        IdAllocator ids = new IdAllocator();
        FlowRunner runner = new FlowRunner(guard, oa, cmd, ids);

        Assembly asm = Assembly.LoadFrom(flowDll);
        Type? type = asm.GetType("Mau.Generated.FL_TextCat");
        if (type == null)
        {
            Console.WriteLine("ERR|TYPE_NOT_FOUND|" + flowDll);
            return 2;
        }
        object? instance = Activator.CreateInstance(type);
        IFlow? flow = instance as IFlow;
        if (flow == null)
        {
            Console.WriteLine("ERR|NOT_IFLOW");
            return 3;
        }

        long id = runner.RegisterFlow(flow, "TextCat");
        Console.WriteLine("flowId=" + id);
        Console.WriteLine("dll=" + flowDll);

        long ownerId = 9001;
        long officeId = oa.Post(ownerId, "TOOL", "text-read", 1200);
        oa.SetStr(officeId, ownerId, "args", "{\"path\":\"D:/Mau/WorkSpace/CatHome5/tools/mau-probe/Program.cs\"}");
        Console.WriteLine("officeId=" + officeId);

        for (int i = 0; i < frames; i++)
        {
            runner.Tick();
            Thread.Sleep(100);
        }

        AuditEvent[] events = AuditStore.Default.Snapshot();
        int fire = 0;
        int transition = 0;
        foreach (AuditEvent ev in events)
        {
            if (ev.Category == "trace.fire")
            {
                fire++;
            }
            else if (ev.Category == "trace.state")
            {
                transition++;
            }
            if (ev.Category.StartsWith("trace.", StringComparison.Ordinal)
                || ev.Category.StartsWith("oa.", StringComparison.Ordinal))
            {
                Console.WriteLine(ev.Category + "|frame=" + ev.Frame + "|" + Props(ev));
            }
        }
        Console.WriteLine("trace.fire=" + fire + " trace.state=" + transition + " audit.total=" + events.Length);

        if (instance is IObservableFlow observable)
        {
            FlowStatusV3 status = observable.GetStatus();
            Console.WriteLine("status.frame=" + status.Frame);
            foreach (string line in status.StateLines)
            {
                Console.WriteLine("state|" + line);
            }
            foreach (WireStatusV3 w in status.WireStatuses)
            {
                if (w.LastTriggerFrame != 0)
                {
                    Console.WriteLine("wire|" + w.Name + "|last=" + w.LastTriggerFrame + "|busy=" + w.Busy);
                }
            }
        }

        return 0;
    }

    private static string Props(AuditEvent ev)
    {
        StringBuilder sb = new StringBuilder();
        for (int i = 0; i < ev.Props.Length; i++)
        {
            if (sb.Length > 0)
            {
                sb.Append(',');
            }
            sb.Append(ev.Props[i].Key).Append('=').Append(ev.Props[i].Value);
        }
        return sb.ToString();
    }
}
