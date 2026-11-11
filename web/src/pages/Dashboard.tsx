import React, { useEffect, useState, useCallback } from 'react';
import { api } from '../lib/api';
import { useWebSocket } from '../lib/useWebSocket';
import type { ClientNode, IPGroup, DeployTask, WSMessage, BatchResultItem } from '../types/spec';
import { ClientCard } from '../components/ClientCard';
import { GroupModal } from '../components/GroupModal';
import { BatchActionBar, BatchResultModal } from '../components/BatchActionBar';
import { DeployWizardModal } from '../components/DeployWizardModal';
import { RestorePointModal } from '../components/RestorePointModal';
import { TaskView } from '../components/TaskView';
import { AuditView } from '../components/AuditView';
import {
  Monitor,
  Radio,
  LogOut,
  RefreshCw,
  Server,
  Activity,
  Layers,
  Plus,
  Edit2,
  Trash2,
  Shield,
} from 'lucide-react';

interface DashboardProps {
  onLogout: () => void;
}

export const Dashboard: React.FC<DashboardProps> = ({ onLogout }) => {
  const [clients, setClients] = useState<ClientNode[]>([]);
  const [groups, setGroups] = useState<IPGroup[]>([]);
  const [tasks, setTasks] = useState<DeployTask[]>([]);
  const [selectedGroup, setSelectedGroup] = useState<string>('');
  const [loading, setLoading] = useState(false);

  // Tab navigation
  const [activeTab, setActiveTab] = useState<'nodes' | 'tasks' | 'audit'>('nodes');

  // Multi-selection state for clients
  const [selectedIPs, setSelectedIPs] = useState<string[]>([]);

  // Modals state
  const [groupModalOpen, setGroupModalOpen] = useState(false);
  const [editingGroup, setEditingGroup] = useState<IPGroup | null>(null);

  const [deployWizardOpen, setDeployWizardOpen] = useState(false);
  const [restoreModalClient, setRestoreModalClient] = useState<ClientNode | null>(null);

  const [batchResults, setBatchResults] = useState<{ title: string; results: BatchResultItem[] } | null>(null);

  // Load full state from REST API
  const refreshAll = useCallback(async () => {
    setLoading(true);
    try {
      const [fetchedClients, fetchedGroups, fetchedTasks] = await Promise.all([
        api.getClients(selectedGroup || undefined),
        api.getGroups(),
        api.getTasks(20),
      ]);
      setClients(fetchedClients);
      setGroups(fetchedGroups);
      setTasks(fetchedTasks);
    } catch (err) {
      console.error('Failed to refresh data:', err);
    } finally {
      setLoading(false);
    }
  }, [selectedGroup]);

  useEffect(() => {
    refreshAll();
  }, [refreshAll]);

  // WebSocket message handler
  const handleWSMessage = useCallback((msg: WSMessage) => {
    switch (msg.event) {
      case 'CLIENT_STATUS_CHANGED': {
        const payload = msg.payload as { client_id: string; to: ClientNode['status'] };
        setClients((prev) =>
          prev.map((c) => (c.client_id === payload.client_id ? { ...c, status: payload.to } : c))
        );
        break;
      }
      case 'CLIENT_UPSERTED': {
        const payload = msg.payload as { client: ClientNode };
        setClients((prev) => {
          const idx = prev.findIndex((c) => c.client_id === payload.client.client_id);
          if (idx >= 0) {
            const next = [...prev];
            next[idx] = payload.client;
            return next;
          }
          return [...prev, payload.client];
        });
        break;
      }
      case 'GROUP_UPDATED': {
        const payload = msg.payload as { group: IPGroup };
        setGroups((prev) => {
          const idx = prev.findIndex((g) => g.group_id === payload.group.group_id);
          if (idx >= 0) {
            const next = [...prev];
            next[idx] = payload.group;
            return next;
          }
          return [...prev, payload.group];
        });
        break;
      }
      case 'DEPLOY_PROGRESS': {
        const payload = msg.payload as {
          task_id: string;
          clients: { ip: string; progress: number; status: ClientNode['status'] }[];
        };
        setClients((prev) =>
          prev.map((c) => {
            const update = payload.clients.find((u) => u.ip === c.current_ip);
            if (update) {
              return { ...c, progress_percent: update.progress, status: update.status };
            }
            return c;
          })
        );
        break;
      }
      case 'TASK_CREATED': {
        const payload = msg.payload as { task: DeployTask };
        setTasks((prev) => [payload.task, ...prev]);
        break;
      }
      default:
        break;
    }
  }, []);

  const { isConnected } = useWebSocket({
    onMessage: handleWSMessage,
    onReconnect: refreshAll,
  });

  const handleLogoutClick = async () => {
    await api.logout();
    onLogout();
  };

  // Single client WOL
  const handleWakeSingle = async (ip: string) => {
    try {
      const res = await api.wakeOnLAN({ ips: [ip] });
      setBatchResults({ title: `喚醒結果 (${ip})`, results: res.results });
    } catch (err) {
      alert(err instanceof Error ? err.message : '喚醒失敗');
    }
  };

  // Batch WOL
  const handleBatchWake = async () => {
    if (selectedIPs.length === 0) return;
    try {
      const res = await api.wakeOnLAN({ ips: selectedIPs });
      setBatchResults({ title: `批次喚醒結果 (${selectedIPs.length} 台)`, results: res.results });
    } catch (err) {
      alert(err instanceof Error ? err.message : '批次喚醒失敗');
    }
  };

  // Delete IP Group
  const handleDeleteGroup = async (groupId: string) => {
    if (!confirm('確定要刪除此群組嗎？電腦節點將轉為未分組，但不會影響設定。')) return;
    try {
      await api.deleteGroup(groupId);
      setSelectedGroup('');
      await refreshAll();
    } catch (err) {
      alert(err instanceof Error ? err.message : '刪除群組失敗');
    }
  };

  return (
    <div className="min-h-screen flex flex-col bg-slate-950 text-slate-100">
      {/* Top Navbar */}
      <header className="h-16 px-6 bg-slate-900 border-b border-slate-800 flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div className="flex items-center justify-center w-9 h-9 bg-blue-600/20 text-blue-400 rounded-lg">
            <Server className="w-5 h-5" />
          </div>
          <div>
            <h1 className="font-bold text-lg leading-tight tracking-tight text-white">back-EZ</h1>
            <p className="text-xs text-slate-400">區域網路還原與映像派送中控台</p>
          </div>
        </div>

        {/* Tab switch */}
        <div className="flex items-center bg-slate-950 p-1 rounded-xl border border-slate-800 text-xs">
          <button
            onClick={() => setActiveTab('nodes')}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg transition-colors ${
              activeTab === 'nodes' ? 'bg-blue-600 text-white font-medium' : 'text-slate-400 hover:text-white'
            }`}
          >
            <Monitor className="w-4 h-4" />
            <span>電腦網格 ({clients.length})</span>
          </button>

          <button
            onClick={() => setActiveTab('tasks')}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg transition-colors ${
              activeTab === 'tasks' ? 'bg-blue-600 text-white font-medium' : 'text-slate-400 hover:text-white'
            }`}
          >
            <Layers className="w-4 h-4" />
            <span>派送任務 ({tasks.length})</span>
          </button>

          <button
            onClick={() => setActiveTab('audit')}
            className={`flex items-center gap-1.5 px-3 py-1.5 rounded-lg transition-colors ${
              activeTab === 'audit' ? 'bg-blue-600 text-white font-medium' : 'text-slate-400 hover:text-white'
            }`}
          >
            <Shield className="w-4 h-4" />
            <span>稽核日誌</span>
          </button>
        </div>

        <div className="flex items-center gap-4">
          <div className="flex items-center gap-2 px-3 py-1 bg-slate-800/80 rounded-full border border-slate-700/60 text-xs">
            <Radio
              className={`w-3.5 h-3.5 ${
                isConnected ? 'text-emerald-400 animate-pulse' : 'text-amber-400'
              }`}
            />
            <span className={isConnected ? 'text-emerald-300' : 'text-amber-300'}>
              {isConnected ? '即時連線中' : '重新連線中'}
            </span>
          </div>

          <button
            onClick={refreshAll}
            disabled={loading}
            className="p-2 text-slate-400 hover:text-white bg-slate-800 hover:bg-slate-700 rounded-lg transition-colors"
            title="手動重新整理"
          >
            <RefreshCw className={`w-4 h-4 ${loading ? 'animate-spin' : ''}`} />
          </button>

          <button
            onClick={handleLogoutClick}
            className="flex items-center gap-1.5 px-3 py-1.5 bg-rose-950/40 hover:bg-rose-900/50 text-rose-300 border border-rose-800/50 rounded-lg text-sm transition-colors"
          >
            <LogOut className="w-4 h-4" />
            <span>登出</span>
          </button>
        </div>
      </header>

      {/* Main Content Area */}
      <main className="flex-1 p-6 space-y-6 max-w-7xl mx-auto w-full">
        {activeTab === 'nodes' && (
          <>
            {/* Toolbar & Filter */}
            <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4 bg-slate-900/60 p-4 rounded-xl border border-slate-800">
              <div className="flex items-center gap-3">
                <span className="text-sm font-medium text-slate-400">IP 群組：</span>
                <select
                  value={selectedGroup}
                  onChange={(e) => setSelectedGroup(e.target.value)}
                  className="bg-slate-800 border border-slate-700 rounded-lg px-3 py-1.5 text-sm text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                >
                  <option value="">全部電腦 ({clients.length})</option>
                  {groups.map((g) => (
                    <option key={g.group_id} value={g.group_id}>
                      {g.group_name} ({g.ip_list.length} 台)
                    </option>
                  ))}
                </select>

                <button
                  onClick={() => {
                    setEditingGroup(null);
                    setGroupModalOpen(true);
                  }}
                  className="flex items-center gap-1 px-2.5 py-1.5 bg-slate-800 hover:bg-slate-700 text-slate-200 border border-slate-700 rounded-lg text-xs font-medium"
                >
                  <Plus className="w-3.5 h-3.5" />
                  <span>新增群組</span>
                </button>

                {selectedGroup && (
                  <>
                    <button
                      onClick={() => {
                        const g = groups.find((grp) => grp.group_id === selectedGroup);
                        if (g) {
                          setEditingGroup(g);
                          setGroupModalOpen(true);
                        }
                      }}
                      className="p-1.5 text-slate-400 hover:text-white bg-slate-800 rounded-lg"
                      title="編輯此群組"
                    >
                      <Edit2 className="w-3.5 h-3.5" />
                    </button>
                    <button
                      onClick={() => handleDeleteGroup(selectedGroup)}
                      className="p-1.5 text-rose-400 hover:bg-rose-950/40 bg-slate-800 rounded-lg"
                      title="刪除此群組"
                    >
                      <Trash2 className="w-3.5 h-3.5" />
                    </button>
                  </>
                )}
              </div>

              <div className="flex items-center gap-4 text-xs text-slate-400">
                <div className="flex items-center gap-1.5">
                  <Activity className="w-4 h-4 text-emerald-400" />
                  <span>
                    在線: {clients.filter((c) => c.status === 'ONLINE_WINDOWS').length} 台
                  </span>
                </div>
                <span>•</span>
                <div>總計: {clients.length} 台</div>
              </div>
            </div>

            {/* Client Grid */}
            <div className="space-y-4">
              {clients.length === 0 ? (
                <div className="p-12 text-center bg-slate-900/40 border border-slate-800/80 rounded-xl">
                  <Monitor className="w-10 h-10 text-slate-600 mx-auto mb-3" />
                  <p className="text-slate-400 text-sm">目前尚無已註冊的電腦節點</p>
                  <p className="text-slate-500 text-xs mt-1">
                    可執行 <code>cmd/mockagent</code> 模擬 20 台測試節點
                  </p>
                </div>
              ) : (
                <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
                  {clients.map((client) => (
                    <ClientCard
                      key={client.client_id}
                      client={client}
                      selected={selectedIPs.includes(client.current_ip)}
                      onSelect={(sel) => {
                        setSelectedIPs((prev) =>
                          sel ? [...prev, client.current_ip] : prev.filter((i) => i !== client.current_ip)
                        );
                      }}
                      onWake={handleWakeSingle}
                      onManagePoints={(c) => setRestoreModalClient(c)}
                    />
                  ))}
                </div>
              )}
            </div>

            {/* Batch Action Bar */}
            <BatchActionBar
              selectedCount={selectedIPs.length}
              totalCount={clients.length}
              onSelectAll={() => setSelectedIPs(clients.map((c) => c.current_ip))}
              onClearSelection={() => setSelectedIPs([])}
              onBatchWake={handleBatchWake}
              onOpenDeployWizard={() => setDeployWizardOpen(true)}
            />
          </>
        )}

        {activeTab === 'tasks' && <TaskView tasks={tasks} onTaskUpdated={refreshAll} />}

        {activeTab === 'audit' && <AuditView />}
      </main>

      {/* Modals */}
      {groupModalOpen && (
        <GroupModal
          initialGroup={editingGroup}
          onClose={() => setGroupModalOpen(false)}
          onSaved={() => {
            setGroupModalOpen(false);
            refreshAll();
          }}
        />
      )}

      {deployWizardOpen && (
        <DeployWizardModal
          clients={clients}
          initialSelectedIPs={selectedIPs}
          activeTasks={tasks}
          onClose={() => setDeployWizardOpen(false)}
          onDeployStarted={(_task) => {
            setDeployWizardOpen(false);
            setActiveTab('tasks');
            refreshAll();
          }}
        />
      )}

      {restoreModalClient && (
        <RestorePointModal
          client={restoreModalClient}
          onClose={() => setRestoreModalClient(null)}
          onUpdated={refreshAll}
        />
      )}

      {batchResults && (
        <BatchResultModal
          title={batchResults.title}
          results={batchResults.results}
          onClose={() => setBatchResults(null)}
        />
      )}
    </div>
  );
};
