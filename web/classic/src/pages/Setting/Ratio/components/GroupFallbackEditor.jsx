import React, { useEffect, useMemo, useState } from 'react';
import {
  Button,
  Card,
  Checkbox,
  Col,
  Form,
  Input,
  Row,
  Spin,
  Switch,
  Typography,
} from '@douyinfe/semi-ui';
import { IconPlus, IconDelete, IconSave } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../../../helpers';

const { Text } = Typography;

function emptyConfig(sourceGroup) {
  return { source_group: sourceGroup, enabled: false, reason: '', rules: [] };
}

function normalizeConfig(config, sourceGroup) {
  const value = config || emptyConfig(sourceGroup);
  return {
    source_group: sourceGroup,
    enabled: Boolean(value.enabled),
    reason: value.reason || '',
    rules: (value.rules || []).map((rule) => ({
      id: rule.id,
      target_group: rule.target_group || '',
      enabled: Boolean(rule.enabled),
      models: Array.isArray(rule.models) ? rule.models : [],
    })),
  };
}

export default function GroupFallbackEditor() {
  const { t } = useTranslation();
  const [data, setData] = useState(null);
  const [sourceGroup, setSourceGroup] = useState('');
  const [config, setConfig] = useState(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);

  const load = async () => {
    setLoading(true);
    try {
      const res = await API.get('/api/group/fallback');
      if (!res?.data?.success) throw new Error(res?.data?.message || t('加载兜底配置失败'));
      const nextData = res.data.data;
      const firstGroup = sourceGroup || nextData.groups?.[0]?.name || '';
      setData(nextData);
      setSourceGroup(firstGroup);
      setConfig(normalizeConfig(nextData.configs?.find((item) => item.source_group === firstGroup), firstGroup));
    } catch (error) {
      showError(error.message || t('加载兜底配置失败'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, []);

  const groups = data?.groups || [];
  const source = groups.find((group) => group.name === sourceGroup);
  const targetGroups = useMemo(() => groups.filter((group) => group.name !== sourceGroup), [groups, sourceGroup]);

  const selectSource = (value) => {
    setSourceGroup(value);
    setConfig(normalizeConfig(data?.configs?.find((item) => item.source_group === value), value));
  };

  const updateRule = (index, patch) => {
    setConfig((current) => {
      if (!current) return current;
      return {
        ...current,
        rules: current.rules.map((rule, itemIndex) => itemIndex === index ? { ...rule, ...patch } : rule),
      };
    });
  };

  const save = async () => {
    if (!config) return;
    setSaving(true);
    try {
      const res = await API.put(`/api/group/fallback/${encodeURIComponent(sourceGroup)}`, config);
      if (!res?.data?.success) throw new Error(res?.data?.message || t('保存兜底配置失败'));
      showSuccess(t('兜底配置保存成功'));
      await load();
    } catch (error) {
      showError(error.message || t('保存兜底配置失败'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Card style={{ marginTop: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 6 }}>
        <Text strong>{t('分组兜底')}</Text>
        <Text type='tertiary' size='small'>{t('路由和计费均使用承接分组，令牌原始分组不变')}</Text>
      </div>
      {loading ? <Spin /> : null}
      {!loading && config ? (
        <>
          <Row gutter={16}>
            <Col xs={24} sm={12}>
              <Form.Select label={t('源分组')} value={sourceGroup} onChange={selectSource} style={{ width: '100%' }}>
                {groups.map((group) => <Form.Select.Option key={group.name} value={group.name}>{group.name}</Form.Select.Option>)}
              </Form.Select>
            </Col>
            <Col xs={24} sm={12}>
              <div style={{ paddingTop: 30 }}>
                <Switch checked={config.enabled} onChange={(enabled) => setConfig({ ...config, enabled })} />
                <Text style={{ marginLeft: 8 }}>{t('启用分组兜底')}</Text>
                <Text type='tertiary' size='small' style={{ marginLeft: 12 }}>{t('源分组倍率')}: {source?.ratio ?? '-'}</Text>
              </div>
            </Col>
          </Row>
          <Form.Input label={t('切换原因')} value={config.reason} maxLength={255} onChange={(reason) => setConfig({ ...config, reason })} />
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', margin: '14px 0 8px' }}>
            <Text>{t('兜底规则')}</Text>
            <Button icon={<IconPlus />} theme='outline' size='small' onClick={() => setConfig({ ...config, rules: [...config.rules, { target_group: '', enabled: false, models: [] }] })}>{t('添加规则')}</Button>
          </div>
          {config.rules.map((rule, index) => {
            const target = groups.find((group) => group.name === rule.target_group);
            const sharedModels = (source?.configured_models || []).filter((name) => target?.configured_models?.includes(name));
            const options = sharedModels.map((name) => ({ label: name, value: name }));
            return (
              <div key={`${rule.id || 'new'}-${index}`} style={{ border: '1px solid var(--semi-color-border)', borderRadius: 6, padding: 12, marginTop: 8 }}>
                <Row gutter={12} type='flex' align='middle'>
                  <Col xs={24} sm={10}>
                    <Form.Select label={t('兜底分组')} value={rule.target_group} onChange={(targetGroup) => updateRule(index, { target_group: targetGroup, models: [] })} style={{ width: '100%' }}>
                      <Form.Select.Option value=''>{t('请选择')}</Form.Select.Option>
                      {targetGroups.map((group) => <Form.Select.Option key={group.name} value={group.name}>{group.name} ({group.ratio})</Form.Select.Option>)}
                    </Form.Select>
                  </Col>
                  <Col xs={18} sm={10}>
                    <Form.Select label={t('承接模型')} multiple filter value={rule.models} onChange={(models) => updateRule(index, { models })} optionList={options} style={{ width: '100%' }} placeholder={t('搜索并选择模型')} />
                  </Col>
                  <Col xs={6} sm={3}>
                    <Checkbox checked={rule.enabled} onChange={(event) => updateRule(index, { enabled: event.target.checked })}>{t('启用')}</Checkbox>
                  </Col>
                  <Col xs={24} sm={1}>
                    <Button type='danger' theme='borderless' icon={<IconDelete />} aria-label={t('删除规则')} onClick={() => setConfig({ ...config, rules: config.rules.filter((_, itemIndex) => itemIndex !== index) })} />
                  </Col>
                </Row>
              </div>
            );
          })}
          <div style={{ textAlign: 'right', marginTop: 14 }}>
            <Button theme='solid' icon={<IconSave />} loading={saving} onClick={save}>{t('保存兜底配置')}</Button>
          </div>
        </>
      ) : null}
    </Card>
  );
}
