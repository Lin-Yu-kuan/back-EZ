import React, { useState } from 'react';
import { api, APIClientError } from '../lib/api';
import { ShieldCheck, Lock, AlertCircle } from 'lucide-react';

interface LoginProps {
  onLoginSuccess: () => void;
}

export const Login: React.FC<LoginProps> = ({ onLoginSuccess }) => {
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!password) return;

    setError(null);
    setLoading(true);

    try {
      await api.login(password);
      onLoginSuccess();
    } catch (err) {
      if (err instanceof APIClientError) {
        if (err.code === 'RATE_LIMITED') {
          setError('登入失敗次數過多，已被限流（5 次／5 分鐘），請稍候再試');
        } else if (err.code === 'UNAUTHORIZED') {
          setError('管理員密碼錯誤');
        } else {
          setError(err.message || '登入失敗');
        }
      } else {
        setError('無法連線至伺服器');
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex items-center justify-center min-h-screen p-4 bg-slate-950">
      <div className="w-full max-w-md p-8 space-y-6 bg-slate-900 border border-slate-800 rounded-xl shadow-2xl">
        <div className="text-center space-y-2">
          <div className="inline-flex items-center justify-center w-12 h-12 bg-blue-600/20 text-blue-400 rounded-full mb-2">
            <ShieldCheck className="w-6 h-6" />
          </div>
          <h1 className="text-2xl font-bold tracking-tight text-white">back-EZ 中控管理系統</h1>
          <p className="text-sm text-slate-400">區域網路還原與映像派送系統</p>
        </div>

        {error && (
          <div className="flex items-center gap-2 p-3 text-sm text-rose-300 bg-rose-950/50 border border-rose-800/60 rounded-lg">
            <AlertCircle className="w-4 h-4 shrink-0" />
            <span>{error}</span>
          </div>
        )}

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-xs font-semibold uppercase tracking-wider text-slate-400 mb-1">
              管理員密碼
            </label>
            <div className="relative">
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="請輸入中控端管理員密碼"
                required
                className="w-full px-4 py-2.5 pl-10 bg-slate-800/80 border border-slate-700 rounded-lg text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-colors"
              />
              <Lock className="w-4 h-4 text-slate-400 absolute left-3.5 top-3.5" />
            </div>
          </div>

          <button
            type="submit"
            disabled={loading}
            className="w-full py-2.5 px-4 bg-blue-600 hover:bg-blue-500 disabled:bg-blue-800 text-white font-medium rounded-lg shadow-lg shadow-blue-600/30 transition-all duration-150 focus:outline-none focus:ring-2 focus:ring-blue-500"
          >
            {loading ? '驗證中...' : '登入中控台'}
          </button>
        </form>

        <div className="pt-4 border-t border-slate-800 text-center">
          <p className="text-xs text-slate-500">back-EZ • Spec-Driven High Performance Architecture</p>
        </div>
      </div>
    </div>
  );
};

