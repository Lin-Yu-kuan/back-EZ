import React, { useState } from 'react';
import type { DeployTask } from '../types/spec';
import { api } from '../lib/api';
import { RotateCcw, XCircle, Clock } from 'lucide-react';

interface TaskViewProps {
  tasks: DeployTask[];
  onTaskUpdated: () => void;
}

export const TaskView: React.FC<TaskViewProps> = ({ tasks, onTaskUpdated }) => {
  const [loadingTask, setLoadingTask] = useState<string | null>(null);

  const handleCancel = async (taskId: string) => {
    setLoadingTask(taskId);
    try {
      await api.cancelDeploy(taskId);
      onTaskUpdated();
    } catch (err) {
      alert(`取消失敗: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setLoadingTask(null);
    }
  };

  const handleRetry = async (taskId: string) => {
    setLoadingTask(taskId);
    try {
      await api.retryDeploy(taskId);
      onTaskUpdated();
    } catch (err) {
      alert(`重試失敗: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setLoadingTask(null);
    }
  };

  return (
    <div className="space-y-4">
      {tasks.length === 0 ? (
        <div className="p-8 text-center bg-slate-900/40 border border-slate-800 rounded-xl text-slate-500 text-sm">
          尚無派送任務記錄
        </div>
      ) : (
        <div className="space-y-3">
          {tasks.map((task) => {
            const isCancellable =
              task.state === 'PENDING_PREPARE' ||
              task.state === 'PREPARING' ||
              task.state === 'READY';
            const isRetriable = task.state === 'FAILED';

            return (
              <div
                key={task.task_id}
                className="p-4 bg-slate-900 border border-slate-800 rounded-xl space-y-3 hover:border-slate-700 transition-colors"
              >
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-3">
                    <span className="font-mono font-bold text-white text-base">{task.task_id}</span>
                    <span
                      className={`text-xs px-2.5 py-0.5 rounded-full font-medium ${
                        task.state === 'SUCCEEDED'
                          ? 'bg-emerald-950 text-emerald-400 border border-emerald-800/60'
                          : task.state === 'FAILED'
                          ? 'bg-rose-950 text-rose-400 border border-rose-800/60'
                          : task.state === 'CANCELLED'
                          ? 'bg-slate-800 text-slate-400 border border-slate-700'
                          : 'bg-blue-950 text-blue-400 border border-blue-800/60'
                      }`}
                    >
                      {task.state}
                    </span>
                  </div>

                  <div className="flex items-center gap-2">
                    {isCancellable && (
                      <button
                        onClick={() => handleCancel(task.task_id)}
                        disabled={loadingTask === task.task_id}
                        className="flex items-center gap-1 px-3 py-1 bg-rose-950/40 hover:bg-rose-900/50 text-rose-300 border border-rose-800/50 rounded-lg text-xs font-medium transition-colors"
                      >
                        <XCircle className="w-3.5 h-3.5" />
                        <span>取消任務</span>
                      </button>
                    )}
                    {isRetriable && (
                      <button
                        onClick={() => handleRetry(task.task_id)}
                        disabled={loadingTask === task.task_id}
                        className="flex items-center gap-1 px-3 py-1 bg-amber-950/40 hover:bg-amber-900/50 text-amber-300 border border-amber-800/50 rounded-lg text-xs font-medium transition-colors"
                      >
                        <RotateCcw className="w-3.5 h-3.5" />
                        <span>重新嘗試</span>
                      </button>
                    )}
                  </div>
                </div>

                <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 text-xs text-slate-400 bg-slate-950/40 p-2.5 rounded-lg border border-slate-800/60">
                  <div>
                    <span className="text-slate-500 block">母機 (Master):</span>
                    <span className="font-mono text-slate-200">{task.master_ip}</span>
                  </div>
                  <div>
                    <span className="text-slate-500 block">目標主機數:</span>
                    <span className="font-mono text-slate-200">{task.target_ips.length} 台</span>
                  </div>
                  <div>
                    <span className="text-slate-500 block">建立時間:</span>
                    <span className="text-slate-300 flex items-center gap-1">
                      <Clock className="w-3 h-3 text-slate-500" />
                      {new Date(task.created_at).toLocaleTimeString()}
                    </span>
                  </div>
                  <div>
                    <span className="text-slate-500 block">映像大小:</span>
                    <span className="font-mono text-slate-300">
                      {(task.image_bytes / (1024 * 1024 * 1024)).toFixed(2)} GB
                    </span>
                  </div>
                </div>

                {task.error_message && (
                  <div className="text-xs text-rose-300 bg-rose-950/30 p-2 rounded border border-rose-800/40">
                    錯誤: {task.error_message}
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
};

