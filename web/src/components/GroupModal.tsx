import React, { useState } from 'react';
import type { IPGroup } from '../types/spec';
import { api, APIClientError } from '../lib/api';
import { parseIPInput } from '../utils/ip';
import { X, AlertCircle } from 'lucide-react';

interface GroupModalProps {
  initialGroup?: IPGroup | null;
  onClose: () => void;
  onSaved: () => void;
}

export const GroupModal: React.FC<GroupModalProps> = ({ initialGroup, onClose, onSaved }) => {
  const [name, setName] = useState(initialGroup?.group_name || '');
  const [ipInput, setIpInput] = useState(initialGroup?.ip_list.join('\n') || '');
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      setError('請輸入群組名稱');
      return;
    }

    const ips = parseIPInput(ipInput);
    if (ips.length === 0) {
      setError('請至少輸入一個有效 IPv4 位址');
      return;
    }

    setError(null);
    setLoading(true);

    try {
      await api.createOrUpdateGroup({
        group_id: initialGroup?.group_id,
        group_name: name.trim(),
        ip_list: ips,
      });
      onSaved();
    } catch (err) {
      if (err instanceof APIClientError) {
        if (err.code === 'IP_IN_OTHER_GROUP') {
          setError('操作失敗：輸入的 IP 中有部分已屬於其他群組（每個 IP 只能屬於單一群組）');
        } else {
          setError(err.message);
        }
      } else {
        setError('儲存群組失敗');
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 bg-black/70 flex items-center justify-center p-4">
      <div className="bg-slate-900 border border-slate-800 rounded-xl max-w-lg w-full p-6 shadow-2xl space-y-5">
        <div className="flex items-center justify-between border-b border-slate-800 pb-3">
          <h3 className="text-lg font-bold text-white">
            {initialGroup ? '編輯 IP 群組' : '新增 IP 群組'}
          </h3>
          <button onClick={onClose} className="text-slate-400 hover:text-white">
            <X className="w-5 h-5" />
          </button>
        </div>

        {error && (
          <div className="flex items-center gap-2 p-3 text-sm text-rose-300 bg-rose-950/50 border border-rose-800/60 rounded-lg">
            <AlertCircle className="w-4 h-4 shrink-0" />
            <span>{error}</span>
          </div>
        )}

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className="block text-xs font-semibold text-slate-400 mb-1">群組名稱</label>
            <input
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="例：電腦教室 A - 第一排"
              required
              className="w-full px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>

          <div>
            <label className="block text-xs font-semibold text-slate-400 mb-1">
              IP 清單或範圍（支援一行一個或範圍如 192.168.1.101-192.168.1.120）
            </label>
            <textarea
              rows={6}
              value={ipInput}
              onChange={(e) => setIpInput(e.target.value)}
              placeholder="192.168.1.101-192.168.1.120&#10;192.168.1.150"
              className="w-full font-mono text-sm px-3 py-2 bg-slate-800 border border-slate-700 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            />
          </div>

          <div className="flex justify-end gap-3 pt-3 border-t border-slate-800">
            <button
              type="button"
              onClick={onClose}
              className="px-4 py-2 text-sm text-slate-300 hover:bg-slate-800 rounded-lg transition-colors"
            >
              取消
            </button>
            <button
              type="submit"
              disabled={loading}
              className="px-4 py-2 text-sm font-medium bg-blue-600 hover:bg-blue-500 disabled:bg-blue-800 text-white rounded-lg transition-colors shadow-lg shadow-blue-600/30"
            >
              {loading ? '儲存中...' : '確認儲存'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};

