using System;
using System.Collections.Generic;
using System.Threading;
using System.Threading.Tasks;
using Xunit;

namespace Mau.Runtime.Tests
{
    /// <summary>
    /// 宿主机制测试——ThreadGuard / IdAllocator / OA / CommandBus / Observer 聚合的确定性行为
    /// </summary>
    public sealed class MechanismTests
    {
        // ═══════════════════════════════════════════
        // ThreadGuard
        // ═══════════════════════════════════════════

        /// <summary>
        /// 主线程调用不抛异常
        /// </summary>
        [Fact]
        public void ThreadGuardAllowsOwnerThread()
        {
            ThreadGuard guard = new ThreadGuard();

            guard.AssertMainThread("Test.Op");
        }

        /// <summary>
        /// 后台线程调用抛 InvalidOperationException
        /// </summary>
        [Fact]
        public void ThreadGuardRejectsForeignThread()
        {
            ThreadGuard guard = new ThreadGuard();
            Exception? captured = null;
            // 🔴 显式线程，不用 Task.Run——Task.Run 在延迟调度下可能内联到调用方的线程池线程，
            // 与测试方法同线程 → 守卫不触发、断言前提不成立（判例 2026-11-06：并行跑时偶发失败，单跑必过）
            Thread worker = new Thread(delegate ()
            {
                try
                {
                    guard.AssertMainThread("Test.Op");
                }
                catch (Exception ex)
                {
                    captured = ex;
                }
            });
            worker.Start();
            worker.Join();

            Assert.NotNull(captured);
            Assert.IsType<InvalidOperationException>(captured);
        }

        // ═══════════════════════════════════════════
        // IdAllocator
        // ═══════════════════════════════════════════

        /// <summary>
        /// 全局 ID 严格递增
        /// </summary>
        [Fact]
        public void IdAllocatorIncrementsGlobally()
        {
            IdAllocator allocator = new IdAllocator();

            long first = allocator.Alloc("Cat", out int firstTypeId);
            long second = allocator.Alloc("Cat", out int secondTypeId);
            long third = allocator.Alloc("Dog", out int thirdTypeId);

            Assert.Equal(1, first);
            Assert.Equal(2, second);
            Assert.Equal(3, third);
            Assert.Equal(1, firstTypeId);
            Assert.Equal(2, secondTypeId);
            Assert.Equal(1, thirdTypeId);
        }

        /// <summary>
        /// 类型计数快照是独立副本
        /// </summary>
        [Fact]
        public void IdAllocatorSnapshotIsIndependent()
        {
            IdAllocator allocator = new IdAllocator();
            allocator.Alloc("Cat", out _);
            allocator.Alloc("Cat", out _);
            allocator.Alloc("Dog", out _);

            IReadOnlyDictionary<string, int> snapshot = allocator.GetTypeCountSnapshot();
            allocator.Alloc("Cat", out _);

            Assert.Equal(2, snapshot["Cat"]);
            Assert.Equal(1, snapshot["Dog"]);
        }

        // ═══════════════════════════════════════════
        // OA
        // ═══════════════════════════════════════════

        /// <summary>
        /// 开放 OA 至少跨过一次执行方认领阶段后才允许超时
        /// </summary>
        [Fact]
        public void OAOpenWorkKeepsOneClaimOpportunityBeforeTimeout()
        {
            ThreadGuard guard = new ThreadGuard();
            OA oa = new OA(guard);
            long ownerId = 1;
            long officeId = oa.Post(ownerId, "TEST", "TIMEOUT", 1);
            bool firstTickObservedOpen = false;
            bool secondTickObservedTimeout = false;
            int observations = 0;

            // [段1] 第一轮保持 Open 供随后执行方认领，第二轮才结算超时
            oa.Tick();
            OfficeState firstStatus = oa.GetStatus(officeId);
            observations = observations + 1;
            if (observations == 1)
            {
                firstTickObservedOpen = firstStatus == OfficeState.Open;
            }
            oa.Tick();
            OfficeState secondStatus = oa.GetStatus(officeId);
            observations = observations + 1;
            if (observations == 2)
            {
                secondTickObservedTimeout = secondStatus == OfficeState.TimeOut;
            }

            Assert.True(firstTickObservedOpen);
            Assert.True(secondTickObservedTimeout);
            Assert.Equal(OfficeState.TimeOut, oa.GetStatus(officeId));
        }
        /// <summary>
        /// OA 的认领、回执和双字典副本边界
        /// </summary>
        [Fact]
        public void OAClaimAndCompleteKeepIndependentCopies()
        {
            ThreadGuard guard = new ThreadGuard();
            OA oa = new OA(guard);
            long ownerId = 1;
            long workerId = 2;

            // [段1] 发布和认领工单后修改调用方载荷
            long officeId = oa.Post(ownerId, "TEST", "COPY", 100);
            oa.SetStr(officeId, ownerId, "input", "input-value");
            List<Office> claimed = oa.ClaimBatch(workerId, new long[] { officeId });
            OfficeData result = OfficeData.Empty();
            result.Strs["output"] = "result-value";
            oa.Complete(officeId, workerId, result);
            result.Strs["output"] = "changed-result";

            // [段2] OA 中的载荷和回执必须保持独立副本
            Office office = oa.GetOffice(officeId);
            Assert.Single(claimed);
            Assert.Equal(OfficeState.Closed, office.Status);
            Assert.Equal("input-value", office.Data.Strs["input"]);
            Assert.Equal("result-value", office.Result.Strs["output"]);
        }
        /// <summary>
        /// 干不了重挂后他人可认领
        /// </summary>
        [Fact]
        public void OARelistReopensForOthers()
        {
            ThreadGuard guard = new ThreadGuard();
            OA oa = new OA(guard);
            long ownerId = 1;
            long workerA = 2;
            long workerB = 3;
            long officeId = oa.Post(ownerId, "TEST", "RE", 100);

            oa.ClaimBatch(workerA, new long[] { officeId });
            oa.Relist(officeId, workerA);
            List<Office> claimedByB = oa.ClaimBatch(workerB, new long[] { officeId });

            Assert.Single(claimedByB);
            Assert.Equal(OfficeState.Work, oa.GetStatus(officeId));
            Assert.Equal(workerB, oa.GetOffice(officeId).ClaimByWorkerId);
        }
        /// <summary>
        /// 执行方回收时释放其全部 Work 工单
        /// </summary>
        [Fact]
        public void OAReleaseByWorkerRelistsWork()
        {
            ThreadGuard guard = new ThreadGuard();
            OA oa = new OA(guard);
            long ownerId = 1;
            long workerId = 2;
            long officeId = oa.Post(ownerId, "TEST", "RELEASE", 100);

            oa.ClaimBatch(workerId, new long[] { officeId });
            oa.ReleaseByWorker(workerId);

            Office released = oa.GetOffice(officeId);
            Assert.Equal(OfficeState.Open, released.Status);
            Assert.Equal(0, released.ClaimByWorkerId);
        }
        /// <summary>
        /// OA 快照统计各状态数量
        /// </summary>
        [Fact]
        public void OAGetSnapshotCountsStates()
        {
            ThreadGuard guard = new ThreadGuard();
            OA oa = new OA(guard);
            long ownerId = 1;
            long workerId = 2;
            long openId = oa.Post(ownerId, "TEST", "OPEN", 100);
            long workId = oa.Post(ownerId, "TEST", "WORK", 100);
            oa.ClaimBatch(workerId, new long[] { workId });
            long doneId = oa.Post(ownerId, "TEST", "DONE", 100);
            oa.ClaimBatch(workerId, new long[] { doneId });
            OfficeData doneResult = OfficeData.Empty();
            doneResult.Strs["status"] = "ok";
            oa.Complete(doneId, workerId, doneResult);

            OAView view = oa.GetSnapshot();

            Assert.Equal(1, view.OpenCount);
            Assert.Equal(1, view.WorkCount);
            Assert.Equal(1, view.ClosedCount);
            Assert.Equal(0, view.TimeoutCount);
            Assert.True(view.Version > 0);
        }
        // ═══════════════════════════════════════════
        // CommandBus
        // ═══════════════════════════════════════════

        /// <summary>
        /// Command 的整数和文本载荷只消费一次
        /// </summary>
        [Fact]
        public void CommandPayloadIsConsumedOnce()
        {
            ThreadGuard guard = new ThreadGuard();
            CommandBus bus = new CommandBus(guard);
            long ownerID = 42;

            // [段1] 注册后写入双轨载荷，冻结后消费
            bus.Register(ownerID, new string[] { "Test_Module_Command" });
            bus.Set("Test_Module_Command", 7, "test");
            bus.SetText("Test_Module_Command", "payload", "test");
            bus.BeginTickInput();
            CommandPack firstEmail = bus.GetCommandEmail(ownerID);

            // [段2] 首次取得值，第二次只得到默认槽位
            CommandPack secondEmail = bus.GetCommandEmail(ownerID);
            Assert.Equal(ownerID, firstEmail.OwnerLongId);
            Assert.Equal(7, firstEmail.CmdValues[0]);
            Assert.Equal("payload", firstEmail.CmdTexts[0]);
            Assert.Equal(0, secondEmail.CmdValues[0]);
            Assert.Null(secondEmail.CmdTexts[0]);
        }

        /// <summary>
        /// Command key 必须使用三段式且注册失败没有部分状态
        /// </summary>
        [Fact]
        public void CommandRegistrationRejectsMalformedBatchAtomically()
        {
            ThreadGuard guard = new ThreadGuard();
            CommandBus bus = new CommandBus(guard);
            long ownerID = 51;

            bus.Register(ownerID, new string[]
            {
                "Cat_TalkCat51_Send",
                "MalformedKey"
            });
            bus.Set("Cat_TalkCat51_Send", 1, "test");
            CommandPack email = bus.GetCommandEmail(ownerID);

            Assert.Equal(0, email.OwnerLongId);
            Assert.Empty(email.CmdKeys);
            Assert.Empty(bus.CmdCache);
        }

        /// <summary>
        /// 完整 key 全局唯一，但不同模块可以复用动作名称
        /// </summary>
        [Fact]
        public void CommandKeyIsUniqueWhileNameCanRepeatAcrossModules()
        {
            ThreadGuard guard = new ThreadGuard();
            CommandBus bus = new CommandBus(guard);

            bus.Register(61, new string[] { "Cat_TalkCat61_Send" });
            bus.Register(62, new string[] { "Cat_TalkCat62_Send" });
            bus.Register(63, new string[] { "Cat_TalkCat61_Send" });
            bus.Set("Cat_TalkCat61_Send", 1, "test");
            bus.Set("Cat_TalkCat62_Send", 2, "test");
            bus.BeginTickInput();

            CommandPack first = bus.GetCommandEmail(61);
            CommandPack second = bus.GetCommandEmail(62);
            CommandPack rejected = bus.GetCommandEmail(63);
            Assert.Equal(1, first.CmdValues[0]);
            Assert.Equal(2, second.CmdValues[0]);
            Assert.Empty(rejected.CmdKeys);
        }

        /// <summary>
        /// 同一注册批次内的重复 key 整批拒绝且不遗留所有权
        /// </summary>
        [Fact]
        public void CommandRegistrationRejectsDuplicateInsideBatchAtomically()
        {
            ThreadGuard guard = new ThreadGuard();
            CommandBus bus = new CommandBus(guard);
            string key = "Cat_Duplicate_Send";

            bus.Register(71, new string[] { key, key });
            bus.Set(key, 1, "test");

            Assert.Empty(bus.GetCommandEmail(71).CmdKeys);
            Assert.Single(bus.GetKeyDic());
            Assert.Empty(bus.CmdCache);
        }

        /// <summary>
        /// Command 快照暴露注册/待消费统计
        /// </summary>
        [Fact]
        public void CommandSnapshotExposesRegistrationStats()
        {
            ThreadGuard guard = new ThreadGuard();
            CommandBus bus = new CommandBus(guard);

            bus.Register(81, new string[] { "Cat_TalkCat81_Send" });
            bus.Set("Cat_TalkCat81_Send", 1, "test");
            CommandSnapshot before = bus.GetSnapshot();
            bus.BeginTickInput();
            CommandSnapshot after = bus.GetSnapshot();

            Assert.Equal(1, after.RegisteredOwnerCount);
            Assert.Equal(1, after.RegisteredKeyCount);
            Assert.Equal(1, before.PendingKeyCount);
            Assert.Equal(1, after.FrozenKeyCount);
            Assert.Equal(0, after.PendingKeyCount);
            Assert.True(after.IsAcceptingInput);
            Assert.Contains("Cat_TalkCat81_Send", after.RegisteredKeys);
        }
    }
}
