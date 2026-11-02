using System;
using System.IO;
using System.Text;

namespace Mau.Runtime
{
    /// <summary>原子写算法可替换的最小文件端口，用于确定性故障测序。</summary>
    public interface CH_I_AtomicFilePort
    {
        /// <summary>建立目录。</summary>
        void CreateDirectory(string path);

        /// <summary>判断文件存在。</summary>
        bool Exists(string path);

        /// <summary>建立并耐久刷新一个新文件。</summary>
        void WriteDurable(string path, byte[] bytes);

        /// <summary>覆盖复制文件。</summary>
        void Copy(string source, string destination);

        /// <summary>以源文件覆盖移动到目标。</summary>
        void MoveReplace(string source, string destination);

        /// <summary>删除文件。</summary>
        void Delete(string path);
    }

    /// <summary>
    /// 明文持久化的统一原子替换器。正式文件旁只保留最后一致副本，
    /// 临时文件永不作为读取来源。
    /// </summary>
    public static class CH_AtomicTextFile
    {
        /// <summary>最后一致副本后缀。</summary>
        public const string LastSuffix = ".last";

        /// <summary>以同目录临时文件原子替换正文并保留上一版。</summary>
        /// <param name="path">正式文件绝对路径</param>
        /// <param name="content">明文正文</param>
        public static void Write(string path, string content)
        {
            Write(path, content, new CH_SystemAtomicFilePort());
        }

        /// <summary>通过显式文件端口执行同一原子替换算法。</summary>
        /// <param name="path">正式文件绝对路径</param>
        /// <param name="content">明文正文</param>
        /// <param name="port">文件操作端口</param>
        public static void Write(string path, string content, CH_I_AtomicFilePort port)
        {
            string fullPath = Path.GetFullPath(path);
            string? directory = Path.GetDirectoryName(fullPath);
            if (directory == null)
            {
                throw new InvalidOperationException(
                    "Atomic file directory is unavailable.");
            }
            port.CreateDirectory(directory);
            string temporary = fullPath + "."
                + Guid.NewGuid().ToString("N") + ".tmp";
            try
            {
                byte[] bytes = new UTF8Encoding(false).GetBytes(content);
                port.WriteDurable(temporary, bytes);
                if (port.Exists(fullPath))
                {
                    port.Copy(fullPath, fullPath + LastSuffix);
                }
                port.MoveReplace(temporary, fullPath);
            }
            finally
            {
                if (port.Exists(temporary))
                {
                    port.Delete(temporary);
                }
            }
        }

        /// <summary>读取正式文件；验证失败时恢复最后一致副本并隔离坏原文。</summary>
        /// <param name="path">正式文件绝对路径</param>
        /// <param name="validator">只验证结构、不产生业务副作用的函数</param>
        /// <param name="recovered">是否发生恢复</param>
        /// <returns>通过验证的正文</returns>
        public static string ReadWithRecovery(string path,
            Func<string, bool> validator, out bool recovered, out string error)
        {
            recovered = false;
            error = "";
            string fullPath;
            try
            {
                fullPath = Path.GetFullPath(path);
            }
            catch (Exception ex)
            {
                error = "路径非法：" + ex.Message;
                return "";
            }
            string primary;
            try
            {
                primary = File.ReadAllText(fullPath, Encoding.UTF8);
            }
            catch (Exception ex)
            {
                error = "读取失败：" + ex.Message;
                return "";
            }
            if (IsValid(primary, validator))
            {
                return primary;
            }
            // 正文无效——先隔离坏原文（隔离失败不影响后续降级）
            string stamp = DateTimeOffset.UtcNow.ToString("yyyyMMdd-HHmmss-fffffff");
            try
            {
                File.Copy(fullPath, fullPath + ".broken-" + stamp, false);
            }
            catch (Exception)
            {
                // 隔离是尽力动作，失败不阻断读取降级
            }
            string backupPath = fullPath + LastSuffix;
            if (!File.Exists(backupPath))
            {
                error = "正文无效且无 .last 副本";
                return "";
            }
            string backup;
            try
            {
                backup = File.ReadAllText(backupPath, Encoding.UTF8);
            }
            catch (Exception ex)
            {
                error = "副本读取失败：" + ex.Message;
                return "";
            }
            if (!IsValid(backup, validator))
            {
                error = "正文与 .last 副本均无效";
                return "";
            }
            string recoveryPath = fullPath + "."
                + Guid.NewGuid().ToString("N") + ".recover";
            try
            {
                byte[] recoveryBytes = new UTF8Encoding(false).GetBytes(backup);
                using (FileStream stream = new FileStream(recoveryPath,
                    FileMode.CreateNew, FileAccess.Write, FileShare.None,
                    4096, FileOptions.WriteThrough))
                {
                    stream.Write(recoveryBytes, 0, recoveryBytes.Length);
                    stream.Flush(true);
                }
                File.Move(recoveryPath, fullPath, true);
            }
            catch (Exception ex)
            {
                error = "副本回写失败：" + ex.Message;
                return "";
            }
            finally
            {
                if (File.Exists(recoveryPath))
                {
                    try
                    {
                        File.Delete(recoveryPath);
                    }
                    catch (Exception)
                    {
                        // 临时文件清理失败不影响结果
                    }
                }
            }
            recovered = true;
            return backup;
        }

        /// <summary>把验证器异常统一视为结构无效。</summary>
        private static bool IsValid(string content,
            Func<string, bool> validator)
        {
            try
            {
                return validator(content);
            }
            catch (Exception exception) when (exception is InvalidDataException
                || exception is ArgumentException
                || exception is System.Text.Json.JsonException)
            {
                LogStore.Add("Mau", 2, "原子文件验证器异常（按结构无效处理）: " + exception.Message, "FILE");
                return false;
            }
        }

        /// <summary>生产环境使用的 System.IO 文件端口。</summary>
        private sealed class CH_SystemAtomicFilePort : CH_I_AtomicFilePort
        {
            /// <summary>建立目录。</summary>
            public void CreateDirectory(string path)
            {
                Directory.CreateDirectory(path);
            }

            /// <summary>判断文件存在。</summary>
            public bool Exists(string path)
            {
                return File.Exists(path);
            }

            /// <summary>建立并强制刷新新文件。</summary>
            public void WriteDurable(string path, byte[] bytes)
            {
                using (FileStream stream = new FileStream(path,
                    FileMode.CreateNew, FileAccess.Write, FileShare.None,
                    4096, FileOptions.WriteThrough))
                {
                    stream.Write(bytes, 0, bytes.Length);
                    stream.Flush(true);
                }
            }

            /// <summary>覆盖复制。</summary>
            public void Copy(string source, string destination)
            {
                File.Copy(source, destination, true);
            }

            /// <summary>覆盖移动。</summary>
            public void MoveReplace(string source, string destination)
            {
                File.Move(source, destination, true);
            }

            /// <summary>删除临时文件。</summary>
            public void Delete(string path)
            {
                File.Delete(path);
            }
        }
    }
}
