import { debounce } from 'lodash'
import React, { FunctionComponent, useMemo, useState } from 'react'

import { Button } from '@sourcegraph/wildcard'
import { Tooltip } from '@sourcegraph/branded/src/components/tooltip/Tooltip'

import { CodeIntelligenceConfigurationPolicyFields, GitObjectType } from '../../../graphql-operations'

import { GitObjectPreview } from './GitObjectPreview'
import styles from './BranchTargetSettings.module.scss'
import TrashIcon from 'mdi-react/TrashIcon'
import classNames from 'classnames'
import { RepositoryPreview } from './RepositoryPreview'

const DEBOUNCED_WAIT = 250

export interface BranchTargetSettingsProps {
    repoId?: string
    policy: CodeIntelligenceConfigurationPolicyFields
    setPolicy: (policy: CodeIntelligenceConfigurationPolicyFields) => void
    disabled: boolean
}

export const BranchTargetSettings: FunctionComponent<BranchTargetSettingsProps> = ({
    repoId,
    policy,
    setPolicy,
    disabled = false,
}) => {
    const [pattern, setPattern] = useState(policy.pattern)
    const debouncedSetPattern = useMemo(() => debounce(value => setPattern(value), DEBOUNCED_WAIT), [])

    return (
        <div className="form-group">
            <div className="form-group">
                <label htmlFor="name">Name</label>
                <input
                    id="name"
                    type="text"
                    className="form-control"
                    value={policy.name}
                    onChange={({ target: { value } }) => setPolicy({ ...policy, name: value })}
                    disabled={disabled}
                    required={true}
                />
                <small className="form-text text-muted">Required.</small>
            </div>

            {!repoId && <TempList policy={policy} setPolicy={setPolicy} disabled={disabled} />}

            <div className="form-group">
                <label htmlFor="type">Type</label>
                <select
                    id="type"
                    className="form-control"
                    value={policy.type}
                    onChange={({ target: { value } }) =>
                        setPolicy({
                            ...policy,
                            type: value as GitObjectType,
                            ...(value !== GitObjectType.GIT_TREE
                                ? {
                                      retainIntermediateCommits: false,
                                      indexIntermediateCommits: false,
                                  }
                                : {}),
                        })
                    }
                    disabled={disabled}
                >
                    <option value="">Select Git object type</option>
                    <option value={GitObjectType.GIT_COMMIT}>HEAD</option>
                    <option value={GitObjectType.GIT_TAG}>Tag</option>
                    <option value={GitObjectType.GIT_TREE}>Branch</option>
                </select>
                <small className="form-text text-muted">Required.</small>
            </div>

            {policy.type !== GitObjectType.GIT_COMMIT && (
                <div className="form-group">
                    <label htmlFor="pattern">Pattern</label>
                    <input
                        id="pattern"
                        type="text"
                        className="form-control text-monospace"
                        value={policy.pattern}
                        onChange={({ target: { value } }) => {
                            setPolicy({ ...policy, pattern: value })
                            debouncedSetPattern(value)
                        }}
                        disabled={disabled}
                        required={true}
                    />
                    <small className="form-text text-muted">Required.</small>
                </div>
            )}

            {repoId && <GitObjectPreview repoId={repoId} type={policy.type} pattern={pattern} />}
        </div>
    )
}

interface TempListProps {
    policy: CodeIntelligenceConfigurationPolicyFields
    setPolicy: (policy: CodeIntelligenceConfigurationPolicyFields) => void
    disabled: boolean
}

const TempList: FunctionComponent<TempListProps> = ({ policy, setPolicy, disabled }) => (
    <div className="mb-2">
        {policy.repositoryPatterns === null ? (
            <>
                This configuration policy applies to all repositories.{' '}
                {!disabled && (
                    <>
                        To restrict the set of repositories to which this configuration applies,{' '}
                        <span
                            className={styles.addRepositoryPattern}
                            onClick={() =>
                                setPolicy({
                                    ...policy,
                                    repositoryPatterns: (policy.repositoryPatterns || []).concat(['']),
                                })
                            }
                        >
                            add a repository pattern
                        </span>
                        .
                    </>
                )}
            </>
        ) : (
            <>
                <div className={styles.grid}>
                    {policy.repositoryPatterns.map((repositoryPattern, i) => (
                        <Temp
                            key={i}
                            index={i}
                            pattern={repositoryPattern}
                            setPattern={value =>
                                setPolicy({
                                    ...policy,
                                    repositoryPatterns: (policy.repositoryPatterns || []).map((v, j) =>
                                        i === j ? value : v
                                    ),
                                })
                            }
                            onDelete={() =>
                                setPolicy({
                                    ...policy,
                                    repositoryPatterns: (policy.repositoryPatterns || []).filter((v, j) => i !== j),
                                })
                            }
                            disabled={disabled}
                        />
                    ))}
                </div>

                {!disabled && (
                    <>
                        <div className="pb-2">
                            <span
                                className={classNames(styles.addRepositoryPattern)}
                                onClick={() => {
                                    setPolicy({
                                        ...policy,
                                        repositoryPatterns: (policy.repositoryPatterns || []).concat(['']),
                                    })
                                }}
                            >
                                Add a repository pattern
                            </span>
                        </div>
                    </>
                )}
            </>
        )}
    </div>
)

interface TempProps {
    index: number
    pattern: string
    setPattern: (value: string) => void
    onDelete: () => void
    disabled: boolean
}

const Temp: FunctionComponent<TempProps> = ({ index, pattern, setPattern, onDelete, disabled }) => {
    const [previewPattern, setPreviewPattern] = useState(pattern)
    const debouncedSetPattern = useMemo(() => debounce(value => setPreviewPattern(value), DEBOUNCED_WAIT), [])

    return (
        <>
            <div className={classNames(styles.name, 'form-group d-flex flex-column mb-0')}>
                <label htmlFor="repo-pattern">Repository pattern #{index + 1}</label>
                <input
                    type="text"
                    className="form-control text-monospace"
                    value={pattern}
                    onChange={({ target }) => {
                        setPattern(target.value)
                        debouncedSetPattern(target.value)
                    }}
                    disabled={disabled}
                    required={true}
                />
            </div>

            <span className={classNames(styles.button, 'd-none d-md-inline')}>
                <Button onClick={() => onDelete()} className="p-0 m-0 pt-4" disabled={disabled}>
                    <Tooltip />
                    <TrashIcon className="icon-inline text-danger" data-tooltip="Delete the repository pattern" />
                </Button>
            </span>

            <div className={classNames(styles.preview, 'form-group d-flex flex-column')}>
                <RepositoryPreview pattern={previewPattern} />
            </div>
        </>
    )
}
