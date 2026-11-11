import React from 'react';
import type { ClientNode } from '../types/spec';
import { Power, AlertTriangle } from 'lucide-react';

interface ClientCardProps {
  client: ClientNode;
  selected: boolean;
  onSelect: (selected: boolean) => void;
  onWake: (ip: string) => void;
  onManagePoints: (client: ClientNode) => void;
}

export const ClientCard: React.FC<ClientCardProps> = ({
  client,
  selected,
  onSelect,
  onWake,
  onManagePoints,
}) => {
  const isOnline = client.status === 'ONLINE_WINDOWS';
  const isPE = client.status === 'READY_FOR_CAST' || client.status === 'ENTERING_PE';
  const isHome = client.os_edition.toLowerCase().includes('home');
  const isMacUnknown = !client.mac_address;

  return (
    <div
      className={`relative p-4 rounded-xl border transition-all ${
        selected
          ? 'bg-blue-950/30 border-blue-500 shadow-lg shadow-blue-900/20'
          : 'bg-slate-900 border-slate-800 hover:border-slate-700'
      }`}
    >
      {/* Header: Checkbox & Name & Status */}
      <div className="flex items-start justify-between gap-2 mb-2">
        <div className="flex items-center gap-2.5">
          <input
            type="checkbox"
            checked={selected}
            onChange={(e) => onSelect(e.target.checked)}
            className="w-4 h-4 rounded bg-slate-800 border-slate-700 text-blue-600 focus:ring-blue-500 focus:ring-offset-slate-900"
          />
          <div>
            <div className="font-semibold text-white leading-tight">{client.hostname || '未命名主機'}</div>
            <div className="text-xs font-mono text-slate-400">{client.current_ip}</div>
          </div>
        </div>

        <span
          className={`text-xs px-2 py-0.5 rounded-full font-medium ${
            isOnline
              ? 'bg-emerald-950 text-emerald-400 border border-emerald-800/60'
              : isPE
              ? 'bg-purple-950 text-purple-400 border border-purple-800/60'
              : client.status === 'ERROR'
              ? 'bg-rose-950 text-rose-400 border border-rose-800/60'
              : 'bg-slate-800 text-slate-400 border border-slate-700'
          }`}
        >
          {client.status}
        </span>
      </div>

      {/* Progress Bar if deploying/verifying */}
      {client.progress_percent !== null && (
        <div className="my-2 space-y-1">
          <div className="flex justify-between text-xs text-blue-400">
            <span>傳輸/校驗中</span>
            <span>{client.progress_percent.toFixed(1)}%</span>
          </div>
          <div className="w-full bg-slate-800 h-1.5 rounded-full overflow-hidden">
            <div
              className="bg-blue-500 h-full rounded-full transition-all duration-300"
              style={{ width: `${client.progress_percent}%` }}
            />
          </div>
        </div>
      )}

      {/* Warnings */}
      <div className="space-y-1 my-2">
        {isMacUnknown && (
          <div className="flex items-center gap-1.5 text-xs text-amber-400 bg-amber-950/40 border border-amber-800/50 px-2 py-1 rounded">
            <AlertTriangle className="w-3.5 h-3.5 shrink-0" />
            <span>MAC 未知（無法喚醒）</span>
          </div>
        )}
        {isHome && (
          <div className="flex items-center gap-1.5 text-xs text-rose-400 bg-rose-950/40 border border-rose-800/50 px-2 py-1 rounded">
            <AlertTriangle className="w-3.5 h-3.5 shrink-0" />
            <span>Home 版（不支援原生還原點）</span>
          </div>
        )}
      </div>

      {/* Details */}
      <div className="text-xs text-slate-400 space-y-1 pt-1 border-t border-slate-800/60">
        <div className="flex justify-between">
          <span>MAC:</span>
          <span className="font-mono text-slate-300">{client.mac_address || '未知'}</span>
        </div>
        <div className="flex justify-between">
          <span>作用中還原點:</span>
          <span className="text-slate-300 truncate max-w-[140px]">
            {client.active_point_id || '無'}
          </span>
        </div>
        <div className="flex justify-between">
          <span>自動還原:</span>
          <span className={client.auto_restore_on_reboot ? 'text-emerald-400' : 'text-slate-500'}>
            {client.auto_restore_on_reboot ? '已啟用' : '停用'}
          </span>
        </div>
      </div>

      {/* Actions */}
      <div className="flex gap-2 mt-3 pt-2 border-t border-slate-800/80">
        <button
          onClick={() => onWake(client.current_ip)}
          disabled={isMacUnknown || isOnline}
          title={isMacUnknown ? 'MAC 未知無法喚醒' : isOnline ? '已在線' : '發送 WOL 喚醒'}
          className="flex-1 flex items-center justify-center gap-1 py-1.5 text-xs font-medium rounded-lg bg-slate-800 hover:bg-slate-700 disabled:opacity-40 disabled:hover:bg-slate-800 text-slate-300 border border-slate-700 transition-colors"
        >
          <Power className="w-3.5 h-3.5" />
          <span>喚醒</span>
        </button>

        <button
          onClick={() => onManagePoints(client)}
          disabled={isHome}
          className="flex-1 py-1.5 text-xs font-medium rounded-lg bg-blue-950/50 hover:bg-blue-900/50 disabled:opacity-40 text-blue-300 border border-blue-800/50 transition-colors"
        >
          還原點管理
        </button>
      </div>
    </div>
  );
};

