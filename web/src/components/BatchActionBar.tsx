import React from 'react';
import type { BatchResultItem } from '../types/spec';
import { Power, Send, CheckSquare, X } from 'lucide-react';

interface BatchActionBarProps {
  selectedCount: number;
  totalCount: number;
  onSelectAll: () => void;
  onClearSelection: () => void;
  onBatchWake: () => void;
  onOpenDeployWizard: () => void;
}

export const BatchActionBar: React.FC<BatchActionBarProps> = ({
  selectedCount,
  totalCount,
  onSelectAll,
  onClearSelection,
  onBatchWake,
  onOpenDeployWizard,
}) => {
  if (selectedCount === 0) return null;

  return (
    <div className="fixed bottom-6 left-1/2 -translate-x-1/2 z-40 bg-slate-900 border border-slate-700/80 px-5 py-3 rounded-2xl shadow-2xl flex items-center gap-4 text-sm text-white">
      <div className="flex items-center gap-2 pr-3 border-r border-slate-700">
        <CheckSquare className="w-4 h-4 text-blue-400" />
        <span className="font-medium">
          已選取 <span className="text-blue-400 font-bold">{selectedCount}</span> / {totalCount} 台
        </span>
      </div>

      <div className="flex items-center gap-2">
        <button
          onClick={onBatchWake}
          className="flex items-center gap-1.5 px-3 py-1.5 bg-slate-800 hover:bg-slate-700 text-slate-200 border border-slate-600 rounded-lg text-xs font-medium transition-colors"
        >
          <Power className="w-3.5 h-3.5 text-amber-400" />
          <span>批次喚醒</span>
        </button>

        <button
          onClick={onOpenDeployWizard}
          className="flex items-center gap-1.5 px-3.5 py-1.5 bg-blue-600 hover:bg-blue-500 text-white rounded-lg text-xs font-semibold shadow-lg shadow-blue-600/30 transition-colors"
        >
          <Send className="w-3.5 h-3.5" />
          <span>開啟派送精靈</span>
        </button>
      </div>

      <div className="flex items-center gap-2 pl-2 border-l border-slate-700 text-xs">
        <button onClick={onSelectAll} className="text-slate-400 hover:text-white">
          全選
        </button>
        <span className="text-slate-600">•</span>
        <button onClick={onClearSelection} className="text-slate-400 hover:text-white flex items-center">
          <X className="w-3.5 h-3.5" />
          <span>取消</span>
        </button>
      </div>
    </div>
  );
};

export const BatchResultModal: React.FC<{
  title: string;
  results: BatchResultItem[];
  onClose: () => void;
}> = ({ title, results, onClose }) => {
  return (
    <div className="fixed inset-0 z-50 bg-black/75 flex items-center justify-center p-4">
      <div className="bg-slate-900 border border-slate-800 rounded-xl max-w-md w-full p-6 shadow-2xl space-y-4">
        <div className="flex items-center justify-between border-b border-slate-800 pb-3">
          <h3 className="font-bold text-white text-base">{title}</h3>
          <button onClick={onClose} className="text-slate-400 hover:text-white">
            <X className="w-5 h-5" />
          </button>
        </div>

        <div className="max-h-80 overflow-y-auto space-y-2 pr-1">
          {results.map((res) => (
            <div
              key={res.ip}
              className={`flex items-center justify-between p-2.5 rounded-lg border text-xs font-mono ${
                res.ok
                  ? 'bg-emerald-950/40 border-emerald-800/50 text-emerald-300'
                  : 'bg-rose-950/40 border-rose-800/50 text-rose-300'
              }`}
            >
              <span>{res.ip}</span>
              <span className="font-sans font-medium">
                {res.ok ? '✓ 成功' : `✗ 失敗: ${res.error?.code || '未知'}`}
              </span>
            </div>
          ))}
        </div>

        <div className="flex justify-end pt-3 border-t border-slate-800">
          <button
            onClick={onClose}
            className="px-4 py-2 bg-slate-800 hover:bg-slate-700 text-white text-sm font-medium rounded-lg transition-colors"
          >
            確定
          </button>
        </div>
      </div>
    </div>
  );
};

