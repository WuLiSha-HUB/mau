using System;
using System.Collections.Generic;
using System.Security.Cryptography;
using System.Text;
using Mau.Runtime;

namespace CatHome4.Http
{
    /// <summary>
    /// Console 口令校验与会话令牌（A328）——主端口管理面的认证面。
    /// 凭据形态：PBKDF2-HMAC-SHA256（60 万次迭代 + 16 字节随机 salt + 32 字节输出），落盘只有不可逆编码串；
    /// 会话令牌：登录成功生成 32 字节随机十六进制串，仅存进程内存（宿主重启即全失效，前端刷新即丢弃）。
    /// 设计权威：design-ch4-console-auth.md。
    /// </summary>
    public sealed class ConsoleAuth
    {
        /// <summary>配置键——口令哈希落盘位置（schema 声明 sensitive，读面掩码）</summary>
        public const string ConfigKey = "auth.console_hash";

        /// <summary>PBKDF2 迭代次数（OWASP 合规档，HMAC-SHA256）</summary>
        private const int Iterations = 600000;

        /// <summary>随机盐字节数</summary>
        private const int SaltBytes = 16;

        /// <summary>派生哈希字节数</summary>
        private const int HashBytes = 32;

        /// <summary>编码串算法标识</summary>
        private const string Scheme = "pbkdf2-sha256";

        /// <summary>有效令牌表——进程内存，重启即清空</summary>
        private readonly HashSet<string> _tokens = new HashSet<string>(StringComparer.Ordinal);

        /// <summary>令牌表锁</summary>
        private readonly object _gate = new object();

        /// <summary>
        /// 是否已设置口令——配置项非空即视为已设置。
        /// </summary>
        /// <returns>true=已设置</returns>
        public bool HasPassword()
        {
            return ReadStored().Length > 0;
        }

        /// <summary>
        /// 设置或修改口令——明文不落盘，只写 PBKDF2 编码串。
        /// </summary>
        /// <param name="plain">口令明文（至少 6 位）</param>
        /// <param name="error">失败原因（成功为空串）</param>
        /// <returns>是否成功</returns>
        public bool SetPassword(string plain, out string error)
        {
            error = "";
            if (plain == null || plain.Length < 6)
            {
                error = "口令至少 6 位";
                return false;
            }
            ConfigStore cfg = null!;
            if (!DataBox.TryResolve<ConfigStore>(out cfg) || cfg == null)
            {
                error = "配置存储未绑定";
                return false;
            }
            ConfigSchema schema = null!;
            bool schemaBound = DataBox.TryResolve<ConfigSchema>(out schema);
            string encoded = Encode(plain);
            string checkError;
            if (!cfg.SetChecked(ConfigKey, encoded, schemaBound ? schema : null, out checkError))
            {
                error = checkError;
                return false;
            }
            return true;
        }

        /// <summary>
        /// 校验口令并签发会话令牌——失败返回空串。
        /// </summary>
        /// <param name="plain">口令明文</param>
        /// <returns>会话令牌（空=失败）</returns>
        public string Login(string plain)
        {
            if (plain == null || plain.Length == 0)
            {
                return "";
            }
            string stored = ReadStored();
            if (stored.Length == 0)
            {
                return "";
            }
            if (!Verify(plain, stored))
            {
                return "";
            }
            byte[] raw = RandomNumberGenerator.GetBytes(32);
            string token = Convert.ToHexString(raw).ToLowerInvariant();
            lock (_gate)
            {
                _tokens.Add(token);
            }
            return token;
        }

        /// <summary>
        /// 令牌是否有效。
        /// </summary>
        /// <param name="token">会话令牌</param>
        /// <returns>true=有效</returns>
        public bool IsAuthenticated(string token)
        {
            if (token == null || token.Length == 0)
            {
                return false;
            }
            lock (_gate)
            {
                return _tokens.Contains(token);
            }
        }

        /// <summary>
        /// 注销令牌——前端刷新即丢弃令牌，本方法供显式登出。
        /// </summary>
        /// <param name="token">会话令牌</param>
        public void Logout(string token)
        {
            if (token == null || token.Length == 0)
            {
                return;
            }
            lock (_gate)
            {
                _tokens.Remove(token);
            }
        }

        /// <summary>
        /// 清空全部会话令牌——改口令后旧会话立即失效（A328：改即生效，不留旧凭据尾巴）。
        /// </summary>
        public void ClearTokens()
        {
            lock (_gate)
            {
                _tokens.Clear();
            }
        }

        /// <summary>
        /// 读取落盘口令哈希——未绑定配置存储或未设置时返回空串。
        /// </summary>
        /// <returns>编码串（空=未设置）</returns>
        private static string ReadStored()
        {
            ConfigStore cfg = null!;
            if (!DataBox.TryResolve<ConfigStore>(out cfg) || cfg == null)
            {
                return "";
            }
            return cfg.Get(ConfigKey, "");
        }

        /// <summary>
        /// 口令编码——pbkdf2-sha256$迭代$盐Base64$哈希Base64。
        /// </summary>
        /// <param name="plain">口令明文</param>
        /// <returns>编码串</returns>
        private static string Encode(string plain)
        {
            byte[] salt = RandomNumberGenerator.GetBytes(SaltBytes);
            byte[] hash = Rfc2898DeriveBytes.Pbkdf2(Encoding.UTF8.GetBytes(plain), salt, Iterations, HashAlgorithmName.SHA256, HashBytes);
            return Scheme + "$" + Iterations.ToString() + "$" + Convert.ToBase64String(salt) + "$" + Convert.ToBase64String(hash);
        }

        /// <summary>
        /// 口令校验——常数时间比较；编码串非法一律判失败（不抛异常）。
        /// </summary>
        /// <param name="plain">口令明文</param>
        /// <param name="stored">落盘编码串</param>
        /// <returns>true=通过</returns>
        private static bool Verify(string plain, string stored)
        {
            string[] parts = stored.Split('$');
            if (parts.Length != 4)
            {
                return false;
            }
            if (!string.Equals(parts[0], Scheme, StringComparison.Ordinal))
            {
                return false;
            }
            int iterations;
            if (!int.TryParse(parts[1], out iterations) || iterations < 1)
            {
                return false;
            }
            byte[] salt;
            byte[] expected;
            try
            {
                salt = Convert.FromBase64String(parts[2]);
                expected = Convert.FromBase64String(parts[3]);
            }
            catch (FormatException)
            {
                return false;
            }
            if (salt.Length == 0 || expected.Length == 0)
            {
                return false;
            }
            byte[] actual = Rfc2898DeriveBytes.Pbkdf2(Encoding.UTF8.GetBytes(plain), salt, iterations, HashAlgorithmName.SHA256, expected.Length);
            return CryptographicOperations.FixedTimeEquals(actual, expected);
        }
    }
}
