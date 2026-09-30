import React, {
  useState,
  useCallback,
  useMemo,
  useRef,
  useEffect,
} from 'react';
import {
  Button,
  Input,
  InputNumber,
  Checkbox,
  Select,
  Spin,
  Switch,
  Typography,
  Popconfirm,
} from '@douyinfe/semi-ui';
import {
  IconPlus,
  IconDelete,
  IconRefresh,
  IconSave,
} from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import CardTable from '../../../../components/common/ui/CardTable';
import { API, showError, showSuccess } from '../../../../helpers';

const { Text } = Typography;

let _idCounter = 0;
const uid = () => `gr_${++_idCounter}`;

function parseJSON(str, fallback) {
  if (!str || !str.trim()) return fallback;
  try {
    return JSON.parse(str);
  } catch {
    return fallback;
  }
}

function buildRows(groupRatioStr, userUsableGroupsStr) {
  const ratioMap = parseJSON(groupRatioStr, {});
  const usableMap = parseJSON(userUsableGroupsStr, {});

  const allNames = new Set([
    ...Object.keys(ratioMap),
    ...Object.keys(usableMap),
  ]);

  return Array.from(allNames).map((name) => ({
    _id: uid(),
    name,
    ratio: ratioMap[name] ?? 1,
    selectable: name in usableMap,
    description: usableMap[name] ?? '',
  }));
}

function emptyFallbackConfig(sourceGroup) {
  return {
    source_group: sourceGroup,
    enabled: false,
    retry_enabled: false,
    retry_max_retries: 3,
    retry_http_status_codes: '429,502-504',
    reason: '',
    rules: [],
  };
}

function hasValidRetryStatusCodes(value) {
  if (!String(value || '').trim()) return false;
  return String(value)
    .split(',')
    .every((segment) => {
      const token = segment.trim().replace(/\s+/g, '');
      const match = token.match(/^(\d{3})(?:-(\d{3}))?$/);
      if (!match) return false;
      const start = Number(match[1]);
      const end = Number(match[2] || match[1]);
      return (
        start >= 100 && end <= 599 && start <= end && (end < 200 || start > 299)
      );
    });
}

function normalizeFallbackConfig(config, sourceGroup) {
  const value = config || emptyFallbackConfig(sourceGroup);
  return {
    source_group: sourceGroup,
    enabled: Boolean(value.enabled),
    retry_enabled: Boolean(value.retry_enabled),
    retry_max_retries: Number(value.retry_max_retries) || 3,
    retry_http_status_codes: value.retry_http_status_codes || '429,502-504',
    reason: value.reason || '',
    rules: (value.rules || []).map((rule) => ({
      id: rule.id,
      target_group: rule.target_group || '',
      enabled: Boolean(rule.enabled),
      models: Array.isArray(rule.models) ? rule.models : [],
    })),
  };
}

function fallbackConfigMap(configs) {
  return (configs || []).reduce((result, config) => {
    result[config.source_group] = normalizeFallbackConfig(
      config,
      config.source_group,
    );
    return result;
  }, {});
}

export function serializeGroupTable(rows) {
  const groupRatio = {};
  const userUsableGroups = {};

  rows.forEach((row) => {
    if (!row.name) return;
    groupRatio[row.name] = row.ratio;
    if (row.selectable) {
      userUsableGroups[row.name] = row.description;
    }
  });

  return {
    GroupRatio: JSON.stringify(groupRatio, null, 2),
    UserUsableGroups: JSON.stringify(userUsableGroups, null, 2),
  };
}

export default function GroupTable({ groupRatio, userUsableGroups, onChange }) {
  const { t } = useTranslation();

  const [rows, setRows] = useState(() =>
    buildRows(groupRatio, userUsableGroups),
  );
  const [fallbackData, setFallbackData] = useState(null);
  const [fallbackConfigs, setFallbackConfigs] = useState({});
  const [fallbackLoading, setFallbackLoading] = useState(true);
  const [savingFallback, setSavingFallback] = useState({});

  const loadFallbackData = useCallback(async () => {
    setFallbackLoading(true);
    try {
      const res = await API.get('/api/group/fallback');
      if (!res?.data?.success) {
        throw new Error(res?.data?.message || t('加载兜底配置失败'));
      }
      setFallbackData(res.data.data);
      setFallbackConfigs(fallbackConfigMap(res.data.data.configs));
    } catch (error) {
      showError(error.message || t('加载兜底配置失败'));
    } finally {
      setFallbackLoading(false);
    }
  }, [t]);

  useEffect(() => {
    loadFallbackData();
  }, [loadFallbackData]);

  const updateFallbackConfig = useCallback((sourceGroup, updater) => {
    setFallbackConfigs((current) => {
      const previous = current[sourceGroup] || emptyFallbackConfig(sourceGroup);
      const next = typeof updater === 'function' ? updater(previous) : updater;
      return { ...current, [sourceGroup]: next };
    });
  }, []);

  const updateFallbackRule = useCallback(
    (sourceGroup, index, patch) => {
      updateFallbackConfig(sourceGroup, (current) => ({
        ...current,
        rules: current.rules.map((rule, ruleIndex) =>
          ruleIndex === index ? { ...rule, ...patch } : rule,
        ),
      }));
    },
    [updateFallbackConfig],
  );

  const saveFallback = useCallback(
    async (sourceGroup) => {
      const config =
        fallbackConfigs[sourceGroup] || emptyFallbackConfig(sourceGroup);
      if (config.retry_enabled) {
        if (
          !Number.isInteger(config.retry_max_retries) ||
          config.retry_max_retries < 1 ||
          config.retry_max_retries > 10
        ) {
          showError(t('重试次数必须是 1 到 10 之间的整数'));
          return;
        }
        if (!hasValidRetryStatusCodes(config.retry_http_status_codes)) {
          showError(t('请使用 429,500-503 这样的 HTTP 状态码格式'));
          return;
        }
      }
      setSavingFallback((current) => ({ ...current, [sourceGroup]: true }));
      try {
        const res = await API.put(
          `/api/group/fallback/${encodeURIComponent(sourceGroup)}`,
          config,
        );
        if (!res?.data?.success) {
          throw new Error(res?.data?.message || t('保存兜底配置失败'));
        }
        setFallbackData(res.data.data);
        setFallbackConfigs(fallbackConfigMap(res.data.data.configs));
        showSuccess(t('兜底配置保存成功'));
      } catch (error) {
        showError(error.message || t('保存兜底配置失败'));
      } finally {
        setSavingFallback((current) => ({
          ...current,
          [sourceGroup]: false,
        }));
      }
    },
    [fallbackConfigs, t],
  );

  // Use functional setRows to keep updateRow/addRow/removeRow referentially
  // stable, preventing columns useMemo from rebuilding on every keystroke
  // which causes the Input cursor to jump to end (cursor reset bug).
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  const emitAndSet = useCallback((updater) => {
    setRows((prev) => {
      const next = typeof updater === 'function' ? updater(prev) : updater;
      onChangeRef.current?.(serializeGroupTable(next));
      return next;
    });
  }, []);

  const updateRow = useCallback(
    (id, field, value) => {
      emitAndSet((prev) =>
        prev.map((r) => (r._id === id ? { ...r, [field]: value } : r)),
      );
    },
    [emitAndSet],
  );

  const addRow = useCallback(() => {
    emitAndSet((prev) => {
      const existingNames = new Set(prev.map((r) => r.name));
      let counter = 1;
      let newName = `group_${counter}`;
      while (existingNames.has(newName)) {
        counter++;
        newName = `group_${counter}`;
      }
      return [
        ...prev,
        {
          _id: uid(),
          name: newName,
          ratio: 1,
          selectable: true,
          description: '',
        },
      ];
    });
  }, [emitAndSet]);

  const removeRow = useCallback(
    (id) => {
      emitAndSet((prev) => prev.filter((r) => r._id !== id));
    },
    [emitAndSet],
  );

  const groupNames = useMemo(() => rows.map((r) => r.name), [rows]);

  const duplicateNames = useMemo(() => {
    const counts = {};
    groupNames.forEach((n) => {
      counts[n] = (counts[n] || 0) + 1;
    });
    return new Set(Object.keys(counts).filter((k) => counts[k] > 1));
  }, [groupNames]);

  // Use ref so column render functions always read the latest duplicate set
  // without adding duplicateNames to columns deps (which would break cursor).
  const duplicateNamesRef = useRef(duplicateNames);
  duplicateNamesRef.current = duplicateNames;

  const renderFallbackEditor = useCallback(
    (record) => {
      const sourceGroup = record.name;
      const config =
        fallbackConfigs[sourceGroup] || emptyFallbackConfig(sourceGroup);
      const groups = fallbackData?.groups || [];
      const source = groups.find((group) => group.name === sourceGroup);
      const targetGroups = groups.filter((group) => group.name !== sourceGroup);

      if (fallbackLoading) {
        return (
          <div className='flex justify-center py-4'>
            <Spin size='small' />
          </div>
        );
      }

      if (!fallbackData) {
        return (
          <div className='py-3'>
            <Text type='tertiary' size='small'>
              {t('暂无兜底数据')}
            </Text>
          </div>
        );
      }

      return (
        <div style={{ padding: '12px 16px 16px' }}>
          <div
            className='flex flex-wrap items-end gap-3'
            style={{
              paddingBottom: 12,
              borderBottom: '1px solid var(--semi-color-border)',
            }}
          >
            <div style={{ flex: '0 1 180px', minWidth: 140 }}>
              <Text strong>{t('兜底配置')}</Text>
              <Text
                type='tertiary'
                size='small'
                ellipsis={{ showTooltip: true }}
                style={{ display: 'block', marginTop: 3 }}
              >
                {sourceGroup}
              </Text>
            </div>

            <div className='flex items-center gap-2' style={{ minHeight: 32 }}>
              <Switch
                size='small'
                checked={config.enabled}
                onChange={(enabled) =>
                  updateFallbackConfig(sourceGroup, (current) => ({
                    ...current,
                    enabled,
                    retry_enabled: enabled ? current.retry_enabled : false,
                  }))
                }
              />
              <Text size='small'>{t('兜底总开关')}</Text>
            </div>

            <div className='flex items-center gap-2' style={{ minHeight: 32 }}>
              <IconRefresh />
              <Switch
                size='small'
                checked={config.retry_enabled}
                disabled={!config.enabled}
                onChange={(retryEnabled) =>
                  updateFallbackConfig(sourceGroup, (current) => ({
                    ...current,
                    retry_enabled: retryEnabled,
                  }))
                }
              />
              <Text size='small'>{t('重试后兜底')}</Text>
            </div>

            {config.retry_enabled && (
              <>
                <div
                  className='flex items-center gap-2'
                  style={{ minHeight: 32 }}
                >
                  <Text
                    type='tertiary'
                    size='small'
                    style={{ whiteSpace: 'nowrap' }}
                  >
                    {t('重试次数')}
                  </Text>
                  <InputNumber
                    size='small'
                    min={1}
                    max={10}
                    value={config.retry_max_retries}
                    onChange={(retryCount) =>
                      updateFallbackConfig(sourceGroup, (current) => ({
                        ...current,
                        retry_max_retries: retryCount ?? 1,
                      }))
                    }
                    style={{ width: 72 }}
                  />
                </div>

                <div
                  className='flex items-center gap-2'
                  style={{ flex: '0 1 230px', minWidth: 200 }}
                >
                  <Text
                    type='tertiary'
                    size='small'
                    style={{ flexShrink: 0, whiteSpace: 'nowrap' }}
                  >
                    {t('上游状态码')}
                  </Text>
                  <Input
                    size='small'
                    value={config.retry_http_status_codes}
                    placeholder='429,502-504'
                    onChange={(statusCodes) =>
                      updateFallbackConfig(sourceGroup, (current) => ({
                        ...current,
                        retry_http_status_codes: statusCodes,
                      }))
                    }
                    style={{ flex: 1, minWidth: 0 }}
                  />
                </div>
              </>
            )}

            <div
              className='flex items-center gap-2'
              style={{ flex: '1 1 360px', minWidth: 320, maxWidth: 520 }}
            >
              <Text
                type='tertiary'
                size='small'
                style={{ flexShrink: 0, whiteSpace: 'nowrap' }}
              >
                {t('切换原因')}
              </Text>
              <Input
                size='small'
                value={config.reason}
                maxLength={255}
                placeholder={t('填写本次启用兜底的原因')}
                onChange={(reason) =>
                  updateFallbackConfig(sourceGroup, (current) => ({
                    ...current,
                    reason,
                  }))
                }
                style={{ flex: 1, minWidth: 0 }}
              />
            </div>

            <Button
              theme='solid'
              size='small'
              icon={<IconSave />}
              loading={Boolean(savingFallback[sourceGroup])}
              onClick={() => saveFallback(sourceGroup)}
            >
              {t('保存兜底配置')}
            </Button>
          </div>

          <div style={{ paddingTop: 4 }}>
            {config.rules.map((rule, index) => {
              const target = groups.find(
                (group) => group.name === rule.target_group,
              );
              const sharedModels = (source?.configured_models || []).filter(
                (modelName) => target?.configured_models?.includes(modelName),
              );

              return (
                <div
                  key={`${rule.id || 'new'}-${index}`}
                  className='flex flex-wrap items-end gap-3'
                  style={{
                    padding: '12px 0',
                    borderBottom: '1px solid var(--semi-color-border)',
                  }}
                >
                  <div style={{ flex: '1 1 220px', minWidth: 200 }}>
                    <Text
                      type='tertiary'
                      size='small'
                      style={{ display: 'block', marginBottom: 4 }}
                    >
                      {t('目标分组')}
                    </Text>
                    <Select
                      size='small'
                      value={rule.target_group}
                      placeholder={t('选择目标分组')}
                      optionList={targetGroups.map((group) => ({
                        value: group.name,
                        label: `${group.name} (${group.ratio})`,
                      }))}
                      onChange={(targetGroup) =>
                        updateFallbackRule(sourceGroup, index, {
                          target_group: targetGroup,
                          models: [],
                        })
                      }
                      style={{ width: '100%' }}
                    />
                  </div>

                  <div style={{ flex: '2 1 360px', minWidth: 260 }}>
                    <Text
                      type='tertiary'
                      size='small'
                      style={{ display: 'block', marginBottom: 4 }}
                    >
                      {t('兜底模型')}
                    </Text>
                    <Select
                      size='small'
                      multiple
                      filter
                      value={rule.models}
                      optionList={sharedModels.map((modelName) => ({
                        value: modelName,
                        label: modelName,
                      }))}
                      placeholder={
                        rule.target_group
                          ? t('选择该目标分组承接的模型')
                          : t('请先选择目标分组')
                      }
                      disabled={!rule.target_group}
                      onChange={(models) =>
                        updateFallbackRule(sourceGroup, index, { models })
                      }
                      style={{ width: '100%' }}
                    />
                  </div>

                  <div
                    className='flex items-center gap-2'
                    style={{ minHeight: 32 }}
                  >
                    <Switch
                      size='small'
                      checked={rule.enabled}
                      onChange={(enabled) =>
                        updateFallbackRule(sourceGroup, index, { enabled })
                      }
                    />
                    <Text size='small'>{t('启用规则')}</Text>
                  </div>

                  <Popconfirm
                    title={t('确认删除该兜底规则？')}
                    position='left'
                    onConfirm={() =>
                      updateFallbackConfig(sourceGroup, (current) => ({
                        ...current,
                        rules: current.rules.filter(
                          (_, ruleIndex) => ruleIndex !== index,
                        ),
                      }))
                    }
                  >
                    <Button
                      type='danger'
                      theme='borderless'
                      size='small'
                      icon={<IconDelete />}
                      aria-label={t('删除规则')}
                    />
                  </Popconfirm>
                </div>
              );
            })}

            {config.rules.length === 0 && (
              <Text
                type='tertiary'
                size='small'
                style={{ display: 'block', padding: '14px 0 8px' }}
              >
                {t('当前分组尚未配置兜底规则')}
              </Text>
            )}

            <Button
              icon={<IconPlus />}
              theme='borderless'
              size='small'
              style={{ marginTop: 8 }}
              onClick={() =>
                updateFallbackConfig(sourceGroup, (current) => ({
                  ...current,
                  rules: [
                    ...current.rules,
                    { target_group: '', enabled: false, models: [] },
                  ],
                }))
              }
            >
              {t('添加兜底规则')}
            </Button>
          </div>
        </div>
      );
    },
    [
      fallbackConfigs,
      fallbackData,
      fallbackLoading,
      saveFallback,
      savingFallback,
      t,
      updateFallbackConfig,
      updateFallbackRule,
    ],
  );

  const columns = useMemo(
    () => [
      {
        title: t('分组名称'),
        dataIndex: 'name',
        key: 'name',
        width: 180,
        render: (_, record) => (
          <Input
            size='small'
            value={record.name}
            status={
              duplicateNamesRef.current.has(record.name) ? 'warning' : undefined
            }
            onChange={(v) => updateRow(record._id, 'name', v)}
          />
        ),
      },
      {
        title: t('倍率'),
        dataIndex: 'ratio',
        key: 'ratio',
        width: 120,
        render: (_, record) => (
          <InputNumber
            size='small'
            min={0}
            step={0.1}
            value={record.ratio}
            style={{ width: '100%' }}
            onChange={(v) => updateRow(record._id, 'ratio', v ?? 0)}
          />
        ),
      },
      {
        title: t('用户可选'),
        dataIndex: 'selectable',
        key: 'selectable',
        width: 90,
        align: 'center',
        render: (_, record) => (
          <Checkbox
            checked={record.selectable}
            onChange={(e) =>
              updateRow(record._id, 'selectable', e.target.checked)
            }
          />
        ),
      },
      {
        title: t('描述'),
        dataIndex: 'description',
        key: 'description',
        render: (_, record) =>
          record.selectable ? (
            <Input
              size='small'
              value={record.description}
              placeholder={t('分组描述')}
              onChange={(v) => updateRow(record._id, 'description', v)}
            />
          ) : (
            <Text type='tertiary' size='small'>
              -
            </Text>
          ),
      },
      {
        title: '',
        key: 'actions',
        width: 50,
        render: (_, record) => (
          <Popconfirm
            title={t('确认删除该分组？')}
            onConfirm={() => removeRow(record._id)}
            position='left'
          >
            <Button
              icon={<IconDelete />}
              type='danger'
              theme='borderless'
              size='small'
            />
          </Popconfirm>
        ),
      },
    ],
    [t, updateRow, removeRow],
  );

  return (
    <div>
      <CardTable
        columns={columns}
        dataSource={rows}
        rowKey='_id'
        hidePagination
        size='small'
        expandedRowRender={renderFallbackEditor}
        hideExpandedColumn={false}
        expandRowByClick={false}
        rowExpandable={(record) => record.name !== 'auto'}
        empty={<Text type='tertiary'>{t('暂无分组，点击下方按钮添加')}</Text>}
      />
      <div className='mt-3 flex justify-center'>
        <Button icon={<IconPlus />} theme='outline' onClick={addRow}>
          {t('添加分组')}
        </Button>
      </div>
      {duplicateNames.size > 0 && (
        <Text type='warning' size='small' className='mt-2 block'>
          {t('存在重复的分组名称：')}
          {Array.from(duplicateNames).join(', ')}
        </Text>
      )}
    </div>
  );
}
