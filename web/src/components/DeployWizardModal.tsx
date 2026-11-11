import React, { useState, useMemo } from 'react';
import type { ClientNode, DeployTask } from '../types/spec';
import { api, APIClientError } from '../lib/api';
import { X, CheckCircle2, XCircle, AlertTriangle, Send } from 'lucide-react';

interface DeployWizardModalProps {
  clients: ClientNode[];
  initialSelectedIPs: string[];
  activeTasks: DeployTask[];
  onClose: () => void;
  onDeployStarted: (task: DeployTask) => void;
}

export const DeployWizardModal: React.FC<DeployWizardModalProps> = ({
  clients,
  initialSelectedIPs,
  activeTasks,
  onClose,
  onDeployStarted,
}) => {
  const [masterIP, setMasterIP] = useState<string>(() => {
    // Default to first online client that is not in initial selection if possible
    const cand = clients.find((c) => !initialSelectedIPs.includes(c.current_ip));
    return cand ? cand.current_ip : clients[0]?.current_ip || '';
  });

  const [targetIPs, setTargetIPs] = useState<string[]>(initialSelectedIPs);
  const [loading, setLoading] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);

  const masterClient = useMemo(() => clients.find((c) => c.current_ip === masterIP), [clients, masterIP]);
  const targetClients = useMemo(
    () => clients.filter((c) => targetIPs.includes(c.current_ip)),
    [clients, targetIPs]
  );

  // Pre-flight checks (SPEC §2.4 & §4.2)
  const checks = useMemo(() => {
    const hasActiveTask = activeTasks.some(
      (t) => t.state !== 'SUCCEEDED' && t.state !== 'FAILED' && t.state !== 'CANCELLED'
    );

    const masterPEReady = !!masterClient?.pe_ready;
    const targetsPEReady = targetClients.length > 0 && targetClients.every((c) => c.pe_ready);

    // NetworkIdentity captured check (SPEC §2.4: targets' NetworkIdentity must be captured)
    const targetsIdentityReady =
      targetClients.length > 0 &&
      targetClients.every((c) => {
        const snap = c.network_snapshot;
        return snap && 'mac_address' in snap && !!snap.mac_address;
      });

    const hasTargets = targetClients.length > 0;
    const masterNotInTargets = !targetIPs.includes(masterIP);

    const allPassed =
      !hasActiveTask &&
      masterPEReady &&
      targetsPEReady &&
      targetsIdentityReady &&
      hasTargets &&
      masterNotInTargets;

    return {
      hasActiveTask,
      masterPEReady,
      targetsPEReady,
      targetsIdentityReady,
      hasTargets,
      masterNotInTargets,
      allPassed,
    };
  }, [masterClient, targetClients, activeTasks, targetIPs, masterIP]);

  const handleStartDeploy = async () => {
    if (!checks.allPassed) return;

    setLoading(true);
    setSubmitError(null);

    try {
      const task = await api.startDeploy(masterIP, targetIPs);
      onDeployStarted(task);
    } catch (err) {
      if (err instanceof APIClientError) {
        setSubmitError(`派送啟動失敗: [${err.code}] ${err.message}`);
      } else {
        setSubmitError('伺服器錯誤，無法啟動派送');
      }
    } finally {
      setLoading(false);
    }
  };

  const toggleTarget = (ip: string) => {
    setTargetIPs((prev) => (prev.includes(ip) ? prev.filter((i) => i !== ip) : [...prev, ip]));
  };

  return (
    <div className="fixed inset-0 z-50 bg-black/75 flex items-center justify-center p-4">
      <div className="bg-slate-900 border border-slate-800 rounded-2xl max-w-2xl w-full p-6 shadow-2xl space-y-6">
        <div className="flex items-center justify-between border-b border-slate-800 pb-3">
          <div className="flex items-center gap-2">
            <Send className="w-5 h-5 text-blue-400" />
            <h3 className="font-bold text-white text-lg">母機直傳群播派送精靈</h3>
          </div>
          <button onClick={onClose} className="text-slate-400 hover:text-white">
            <X className="w-5 h-5" />
          </button>
        </div>

        {submitError && (
          <div className="p-3 text-sm text-rose-300 bg-rose-950/50 border border-rose-800/60 rounded-lg">
            {submitError}
          </div>
        )}

        {/* Step 1: Master Selection */}
        <div className="space-y-2">
          <label className="block text-xs font-semibold text-slate-400">1. 選擇母機（Master / Sender）</label>
          <select
            value={masterIP}
            onChange={(e) => setMasterIP(e.target.value)}
            className="w-full bg-slate-800 border border-slate-700 rounded-lg px-3 py-2 text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
          >
            {clients.map((c) => (
              <option key={c.client_id} value={c.current_ip}>
                {c.hostname} ({c.current_ip}) {c.pe_ready ? '• PE 就緒' : '• PE 未就緒'}
              </option>
            ))}
          </select>
        </div>

        {/* Step 2: Target Selection Summary */}
        <div className="space-y-2">
          <div className="flex justify-between items-center">
            <label className="text-xs font-semibold text-slate-400">
              2. 選擇目標子機（已選取 {targetClients.length} 台）
            </label>
          </div>
          <div className="max-h-36 overflow-y-auto bg-slate-950/60 p-3 rounded-lg border border-slate-800 grid grid-cols-2 sm:grid-cols-3 gap-2">
            {clients
              .filter((c) => c.current_ip !== masterIP)
              .map((c) => {
                const isChecked = targetIPs.includes(c.current_ip);
                return (
                  <label
                    key={c.client_id}
                    className={`flex items-center gap-2 p-2 rounded border text-xs cursor-pointer select-none transition-colors ${
                      isChecked
                        ? 'bg-blue-950/40 border-blue-600/60 text-white'
                        : 'bg-slate-900 border-slate-800 text-slate-400 hover:border-slate-700'
                    }`}
                  >
                    <input
                      type="checkbox"
                      checked={isChecked}
                      onChange={() => toggleTarget(c.current_ip)}
                      className="rounded bg-slate-800 border-slate-700 text-blue-600"
                    />
                    <span className="truncate">{c.hostname} ({c.current_ip})</span>
                  </label>
                );
              })}
          </div>
        </div>

        {/* Step 3: Pre-flight Verification checklist */}
        <div className="space-y-2 bg-slate-950/50 p-4 rounded-xl border border-slate-800 text-xs space-y-2">
          <div className="font-semibold text-slate-300 mb-2">3. 派送前置條件檢核（Pre-flight Checks）</div>

          <div className="flex items-center gap-2">
            {checks.masterPEReady ? (
              <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" />
            ) : (
              <XCircle className="w-4 h-4 text-rose-400 shrink-0" />
            )}
            <span className={checks.masterPEReady ? 'text-slate-300' : 'text-rose-300'}>
              母機 PE 映像與引導已就緒 (pe_ready: true)
            </span>
          </div>

          <div className="flex items-center gap-2">
            {checks.targetsPEReady ? (
              <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" />
            ) : (
              <XCircle className="w-4 h-4 text-rose-400 shrink-0" />
            )}
            <span className={checks.targetsPEReady ? 'text-slate-300' : 'text-rose-300'}>
              所有目標子機 PE 映像已就緒
            </span>
          </div>

          <div className="flex items-center gap-2">
            {checks.targetsIdentityReady ? (
              <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" />
            ) : (
              <XCircle className="w-4 h-4 text-rose-400 shrink-0" />
            )}
            <span className={checks.targetsIdentityReady ? 'text-slate-300' : 'text-rose-300'}>
              所有目標機網路身分 (NetworkIdentity) 已收妥
            </span>
          </div>

          <div className="flex items-center gap-2">
            {!checks.hasActiveTask ? (
              <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" />
            ) : (
              <XCircle className="w-4 h-4 text-rose-400 shrink-0" />
            )}
            <span className={!checks.hasActiveTask ? 'text-slate-300' : 'text-rose-300'}>
              無其他進行中的派送任務
            </span>
          </div>

          <div className="flex items-center gap-2">
            {checks.hasTargets && checks.masterNotInTargets ? (
              <CheckCircle2 className="w-4 h-4 text-emerald-400 shrink-0" />
            ) : (
              <XCircle className="w-4 h-4 text-rose-400 shrink-0" />
            )}
            <span className={checks.hasTargets && checks.masterNotInTargets ? 'text-slate-300' : 'text-rose-300'}>
              目標清單有效且不包含母機
            </span>
          </div>
        </div>

        {/* Warning / Secondary confirmation notice */}
        <div className="p-3 bg-amber-950/30 border border-amber-800/40 rounded-lg flex items-start gap-2.5 text-xs text-amber-300">
          <AlertTriangle className="w-4 h-4 shrink-0 text-amber-400 mt-0.5" />
          <span>
            <b>派送警告與二次確認</b>：派送成功後將覆蓋子機磁碟並清除舊差分還原點鏈，建立全新基準點（SPEC 決策 D8）。子機原始 IP 與電腦名稱將依已備份之 NetworkIdentity 自動還原。
          </span>
        </div>

        {/* Footer Actions */}
        <div className="flex items-center justify-end gap-3 pt-3 border-t border-slate-800">
          <button
            type="button"
            onClick={onClose}
            className="px-4 py-2 text-sm text-slate-300 hover:bg-slate-800 rounded-lg transition-colors"
          >
            取消
          </button>
          <button
            type="button"
            disabled={!checks.allPassed || loading}
            onClick={handleStartDeploy}
            className="flex items-center gap-2 px-5 py-2.5 bg-blue-600 hover:bg-blue-500 disabled:bg-slate-800 disabled:text-slate-500 disabled:cursor-not-allowed text-white text-sm font-semibold rounded-lg shadow-lg shadow-blue-600/30 transition-all"
          >
            <Send className="w-4 h-4" />
            <span>{loading ? '啟動中...' : checks.allPassed ? '開始執行派送' : '前置條件未滿足（無法派送）'}</span>
          </button>
        </div>
      </div>
    </div>
  );
};
