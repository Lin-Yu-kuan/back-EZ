import React, { useEffect, useState } from 'react';
import type { ClientNode, RestorePoint } from '../types/spec';
import { api, APIClientError } from '../lib/api';
import { X, Layers, Plus, RotateCcw, Trash2, AlertCircle } from 'lucide-react';

interface RestorePointModalProps {
  client: ClientNode;
  onClose: () => void;
  onUpdated: () => void;
}

export const RestorePointModal: React.FC<RestorePointModalProps> = ({
  client,
  onClose,
  onUpdated,
}) => {
  const [points, setPoints] = useState<RestorePoint[]>([]);
  const [activeID, setActiveID] = useState<string | null>(client.active_point_id);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // New point form
  const [showCreate, setShowCreate] = useState(false);
  const [newName, setNewName] = useState('');
  const [newDesc, setNewDesc] = useState('');

  const fetchPoints = async () => {
    setLoading(true);
    try {
      const res = await api.getRestorePoints(client.current_ip);
      setPoints(res.restore_points);
      setActiveID(res.active_point_id);
    } catch (err) {
      setError(err instanceof Error ? err.message : '載入還原點失敗');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchPoints();
  }, [client.current_ip]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newName.trim()) return;

    setLoading(true);
    setError(null);

    try {
      await api.restorePointAction({
        action: 'create',
        name: newName.trim(),
        description: newDesc.trim(),
        targets: { ips: [client.current_ip] },
      });
      setShowCreate(false);
      setNewName('');
      setNewDesc('');
      await fetchPoints();
      onUpdated();
    } catch (err) {
      if (err instanceof APIClientError) {
        if (err.code === 'RESTORE_POINT_LIMIT') {
          setError('新增失敗：還原點差分深度已達上限 5 層（SPEC §2.2）');
        } else {
          setError(err.message);
        }
      } else {
        setError('新增還原點失敗');
      }
    } finally {
      setLoading(false);
    }
  };

  const handleSwitch = async (pointId: string) => {
    if (!confirm(`確定要將 ${client.hostname} 切換至還原點 ${pointId} 嗎？`)) return;

    setLoading(true);
    setError(null);

    try {
      await api.restorePointAction({
        action: 'switch',
        point_id: pointId,
        targets: { ips: [client.current_ip] },
      });
      await fetchPoints();
      onUpdated();
    } catch (err) {
      setError(err instanceof Error ? err.message : '切換還原點失敗');
    } finally {
      setLoading(false);
    }
  };

  const handleDelete = async (pointId: string) => {
    if (!confirm(`確定要刪除還原點 ${pointId} 嗎？此操作不可逆！`)) return;

    setLoading(true);
    setError(null);

    try {
      await api.restorePointAction({
        action: 'delete',
        point_id: pointId,
        targets: { ips: [client.current_ip] },
      });
      await fetchPoints();
      onUpdated();
    } catch (err) {
      if (err instanceof APIClientError && err.code === 'INVALID_STATE') {
        setError('刪除失敗：禁止刪除當前使用中 (is_active) 的還原點！');
      } else {
        setError(err instanceof Error ? err.message : '刪除還原點失敗');
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 bg-black/75 flex items-center justify-center p-4">
      <div className="bg-slate-900 border border-slate-800 rounded-2xl max-w-xl w-full p-6 shadow-2xl space-y-5">
        <div className="flex items-center justify-between border-b border-slate-800 pb-3">
          <div className="flex items-center gap-2">
            <Layers className="w-5 h-5 text-blue-400" />
            <h3 className="font-bold text-white text-base">
              {client.hostname} 還原點管理 ({client.current_ip})
            </h3>
          </div>
          <button onClick={onClose} className="text-slate-400 hover:text-white">
            <X className="w-5 h-5" />
          </button>
        </div>

        {error && (
          <div className="flex items-center gap-2 p-3 text-xs text-rose-300 bg-rose-950/50 border border-rose-800/60 rounded-lg">
            <AlertCircle className="w-4 h-4 shrink-0" />
            <span>{error}</span>
          </div>
        )}

        {/* Restore Point List */}
        <div className="space-y-2 max-h-64 overflow-y-auto pr-1">
          {points.length === 0 ? (
            <div className="p-6 text-center text-slate-500 text-xs">目前無還原點</div>
          ) : (
            points.map((rp) => {
              const isActive = rp.point_id === activeID || rp.is_active;
              return (
                <div
                  key={rp.point_id}
                  className={`p-3 rounded-xl border text-xs space-y-1.5 transition-colors ${
                    isActive
                      ? 'bg-blue-950/40 border-blue-500/60'
                      : 'bg-slate-950/60 border-slate-800 hover:border-slate-700'
                  }`}
                >
                  <div className="flex items-center justify-between">
                    <div className="flex items-center gap-2">
                      <span className="font-bold text-white text-sm">{rp.name}</span>
                      <span className="font-mono text-slate-400">({rp.point_id})</span>
                      {isActive && (
                        <span className="px-2 py-0.5 bg-blue-600 text-white rounded-full text-[10px] font-semibold">
                          作用中 (Active)
                        </span>
                      )}
                    </div>

                    <div className="flex items-center gap-1.5">
                      {!isActive && (
                        <button
                          onClick={() => handleSwitch(rp.point_id)}
                          disabled={loading}
                          className="flex items-center gap-1 px-2.5 py-1 bg-slate-800 hover:bg-slate-700 text-slate-200 rounded text-xs transition-colors"
                        >
                          <RotateCcw className="w-3 h-3 text-blue-400" />
                          <span>切換</span>
                        </button>
                      )}
                      {!isActive && !rp.is_base && (
                        <button
                          onClick={() => handleDelete(rp.point_id)}
                          disabled={loading}
                          className="p-1 hover:bg-rose-950 text-slate-400 hover:text-rose-400 rounded transition-colors"
                          title="刪除還原點"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </button>
                      )}
                    </div>
                  </div>

                  <div className="text-slate-400 flex items-center justify-between text-[11px]">
                    <span>深度: {rp.depth} / 5</span>
                    <span>檔案: {rp.vhdx_file}</span>
                    <span>建置者: {rp.created_by}</span>
                  </div>

                  {rp.description && (
                    <div className="text-slate-400 text-[11px] bg-slate-900/80 p-1.5 rounded">
                      {rp.description}
                    </div>
                  )}
                </div>
              );
            })
          )}
        </div>

        {/* Create form toggle */}
        {showCreate ? (
          <form onSubmit={handleCreate} className="p-4 bg-slate-950/70 border border-slate-800 rounded-xl space-y-3">
            <h4 className="font-semibold text-white text-xs">新增秒級還原點（差分層）</h4>
            <input
              type="text"
              placeholder="還原點名稱（例：期末上機考乾淨環境）"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              required
              className="w-full px-3 py-1.5 bg-slate-800 border border-slate-700 rounded text-xs text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
            <input
              type="text"
              placeholder="備註說明（可選）"
              value={newDesc}
              onChange={(e) => setNewDesc(e.target.value)}
              className="w-full px-3 py-1.5 bg-slate-800 border border-slate-700 rounded text-xs text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setShowCreate(false)}
                className="px-3 py-1 text-xs text-slate-400 hover:text-white"
              >
                取消
              </button>
              <button
                type="submit"
                disabled={loading}
                className="px-3 py-1 bg-blue-600 hover:bg-blue-500 text-white rounded text-xs font-medium"
              >
                確認新增
              </button>
            </div>
          </form>
        ) : (
          <button
            onClick={() => setShowCreate(true)}
            disabled={points.length >= 5}
            className="w-full py-2 flex items-center justify-center gap-1.5 border border-dashed border-slate-700 hover:border-slate-500 text-slate-300 disabled:opacity-40 rounded-xl text-xs font-medium transition-colors"
          >
            <Plus className="w-4 h-4" />
            <span>新增還原點 {points.length >= 5 ? '(已達 5 層上限)' : ''}</span>
          </button>
        )}

        <div className="flex justify-end pt-3 border-t border-slate-800">
          <button
            onClick={onClose}
            className="px-4 py-2 bg-slate-800 hover:bg-slate-700 text-white text-xs font-medium rounded-lg"
          >
            關閉
          </button>
        </div>
      </div>
    </div>
  );
};

