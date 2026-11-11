import React, { useEffect, useState } from 'react';
import { api } from '../lib/api';
import { Shield, Clock, CheckCircle2, XCircle } from 'lucide-react';

interface AuditItem {
  id: number;
  timestamp: string;
  actor: string;
  action: string;
  target_ip?: string;
  target_client_id?: string;
  details?: string;
  success: boolean;
}

export const AuditView: React.FC = () => {
  const [logs, setLogs] = useState<AuditItem[]>([]);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    const fetchLogs = async () => {
      setLoading(true);
      try {
        const data = await api.getAuditLogs(50);
        setLogs(data as AuditItem[]);
      } catch (err) {
        console.error('Failed to load audit logs:', err);
      } finally {
        setLoading(false);
      }
    };
    fetchLogs();
  }, []);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h3 className="text-base font-semibold text-white flex items-center gap-2">
          <Shield className="w-4 h-4 text-blue-400" />
          <span>安全稽核日誌 (Audit Log)</span>
        </h3>
        {loading && <span className="text-xs text-slate-400">載入中...</span>}
      </div>

      <div className="bg-slate-900 border border-slate-800 rounded-xl overflow-hidden shadow">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs text-slate-300">
            <thead className="bg-slate-950/70 text-slate-400 uppercase tracking-wider text-[11px] border-b border-slate-800">
              <tr>
                <th className="px-4 py-3">時間</th>
                <th className="px-4 py-3">操作者</th>
                <th className="px-4 py-3">操作項目</th>
                <th className="px-4 py-3">目標 IP / Client</th>
                <th className="px-4 py-3">結果</th>
                <th className="px-4 py-3">詳細</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-800/60 font-mono">
              {logs.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-4 py-8 text-center text-slate-500 font-sans">
                    尚無稽核日誌記錄
                  </td>
                </tr>
              ) : (
                logs.map((log) => (
                  <tr key={log.id} className="hover:bg-slate-800/40 transition-colors">
                    <td className="px-4 py-3 text-slate-400 flex items-center gap-1">
                      <Clock className="w-3 h-3 text-slate-600" />
                      {new Date(log.timestamp).toLocaleString()}
                    </td>
                    <td className="px-4 py-3 text-white font-sans">{log.actor}</td>
                    <td className="px-4 py-3 text-blue-400 font-semibold">{log.action}</td>
                    <td className="px-4 py-3 text-slate-300">
                      {log.target_ip || log.target_client_id || '-'}
                    </td>
                    <td className="px-4 py-3">
                      {log.success ? (
                        <span className="inline-flex items-center gap-1 text-emerald-400">
                          <CheckCircle2 className="w-3.5 h-3.5" />
                          <span>成功</span>
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1 text-rose-400">
                          <XCircle className="w-3.5 h-3.5" />
                          <span>失敗</span>
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-slate-400 truncate max-w-xs">{log.details || '-'}</td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
};

