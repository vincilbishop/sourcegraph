import * as React from 'react'

import classNames from 'classnames'
import { escapeRegExp } from 'lodash'
import { RouteComponentProps } from 'react-router-dom'
import { Observable, Subject } from 'rxjs'
import { map } from 'rxjs/operators'

import { Form } from '@sourcegraph/branded/src/components/Form'
import { createAggregateError, numberWithCommas, pluralize, memoizeObservable } from '@sourcegraph/common'
import { gql } from '@sourcegraph/http-client'
import { Scalars, SearchPatternType } from '@sourcegraph/shared/src/graphql-operations'
import * as GQL from '@sourcegraph/shared/src/schema'
import { buildSearchURLQuery } from '@sourcegraph/shared/src/util/url'
import { Button, ButtonGroup, Link, CardHeader, CardBody, Card, Input, Label, Tooltip } from '@sourcegraph/wildcard'

import { queryGraphQL } from '../../backend/graphql'
import { FilteredConnection } from '../../components/FilteredConnection'
import { PageTitle } from '../../components/PageTitle'
import { Timestamp } from '../../components/time/Timestamp'
import { PersonLink } from '../../person/PersonLink'
import { quoteIfNeeded, searchQueryForRepoRevision } from '../../search'
import { eventLogger } from '../../tracking/eventLogger'
import { UserAvatar } from '../../user/UserAvatar'

import { RepositoryStatsAreaPageProps } from './RepositoryStatsArea'

import styles from './RepositoryStatsContributorsPage.module.scss'

interface QuerySpec {
    revisionRange: string | null
    after: string | null
    path: string | null
}

interface RepositoryContributorNodeProps extends QuerySpec {
    node: GQL.IRepositoryContributor
    repoName: string
    globbing: boolean
}

const RepositoryContributorNode: React.FunctionComponent<React.PropsWithChildren<RepositoryContributorNodeProps>> = ({
    node,
    repoName,
    revisionRange,
    after,
    path,
    globbing,
}) => {
    const commit = node.commits.nodes[0] as GQL.IGitCommit | undefined

    const query: string = [
        searchQueryForRepoRevision(repoName, globbing),
        'type:diff',
        `author:${quoteIfNeeded(node.person.email)}`,
        after ? `after:${quoteIfNeeded(after)}` : '',
        path ? `file:${quoteIfNeeded(escapeRegExp(path))}` : '',
    ]
        .join(' ')
        .replace(/\s+/, ' ')

    return (
        <li className={classNames('list-group-item py-2', styles.repositoryContributorNode)}>
            <div className={styles.person}>
                <UserAvatar inline={true} className="mr-2" user={node.person} />
                <PersonLink userClassName="font-weight-bold" person={node.person} />
            </div>
            <div className={styles.commits}>
                <div className={styles.commit}>
                    {commit && (
                        <>
                            <Timestamp date={commit.author.date} />:{' '}
                            <Tooltip content="Most recent commit by contributor" placement="bottom">
                                <Link to={commit.url} className="repository-contributor-node__commit-subject">
                                    {commit.subject}
                                </Link>
                            </Tooltip>
                        </>
                    )}
                </div>
                <div className={styles.count}>
                    <Tooltip
                        content={
                            revisionRange?.includes('..')
                                ? 'All commits will be shown (revision end ranges are not yet supported)'
                                : null
                        }
                        placement="left"
                    >
                        <Link
                            to={`/search?${buildSearchURLQuery(query, SearchPatternType.literal, false)}`}
                            className="font-weight-bold"
                        >
                            {numberWithCommas(node.count)} {pluralize('commit', node.count)}
                        </Link>
                    </Tooltip>
                </div>
            </div>
        </li>
    )
}

const queryRepositoryContributors = memoizeObservable(
    (args: {
        repo: Scalars['ID']
        first?: number
        revisionRange?: string
        after?: string
        path?: string
    }): Observable<GQL.IRepositoryContributorConnection> =>
        queryGraphQL(
            gql`
                query RepositoryContributors(
                    $repo: ID!
                    $first: Int
                    $revisionRange: String
                    $after: String
                    $path: String
                ) {
                    node(id: $repo) {
                        ... on Repository {
                            contributors(first: $first, revisionRange: $revisionRange, after: $after, path: $path) {
                                nodes {
                                    person {
                                        name
                                        displayName
                                        email
                                        avatarURL
                                        user {
                                            username
                                            url
                                        }
                                    }
                                    count
                                    commits(first: 1) {
                                        nodes {
                                            oid
                                            abbreviatedOID
                                            url
                                            subject
                                            author {
                                                date
                                            }
                                        }
                                    }
                                }
                                totalCount
                                pageInfo {
                                    hasNextPage
                                }
                            }
                        }
                    }
                }
            `,
            args
        ).pipe(
            map(({ data, errors }) => {
                if (!data || !data.node || !(data.node as GQL.IRepository).contributors || errors) {
                    throw createAggregateError(errors)
                }
                return (data.node as GQL.IRepository).contributors
            })
        ),
    args =>
        `${args.repo}:${String(args.first)}:${String(args.revisionRange)}:${String(args.after)}:${String(args.path)}`
)

const equalOrEmpty = (a: string | null, b: string | null): boolean => a === b || (!a && !b)

interface Props extends RepositoryStatsAreaPageProps, RouteComponentProps<{}> {
    globbing: boolean
}

class FilteredContributorsConnection extends FilteredConnection<
    GQL.IRepositoryContributor,
    Pick<RepositoryContributorNodeProps, 'repoName' | 'revisionRange' | 'after' | 'path' | 'globbing'>
> {}

const contributorsPageInputIds: Record<keyof QuerySpec, string> = {
    revisionRange: 'repository-stats-contributors-page__revision-range',
    after: 'repository-stats-contributors-page__after',
    path: 'repository-stats-contributors-page__path',
}

/** A page that shows a repository's contributors. */
export const RepositoryStatsContributorsPage: React.FunctionComponent<Props> = ({
    location,
    history,
    repo,
    globbing,
}) => {
    // Get state from query params
    const getDerivedState = React.useCallback(
        (_location: typeof location = location): QuerySpec => {
            const query = new URLSearchParams(_location.search)
            return {
                revisionRange: query.get('revisionRange'),
                after: query.get('after'),
                path: query.get('path'),
            }
        },
        [location]
    )

    // Get query params from state
    const getUrlQuery = React.useCallback((spec: QuerySpec): string => {
        const search = new URLSearchParams()
        for (const [key, value] of Object.entries(spec)) {
            if (value) {
                search.set(key, value)
            }
        }
        return search.toString()
    }, [])

    const [state, setState] = React.useState<QuerySpec>(getDerivedState())
    const [bufferState, setBufferState] = React.useState<QuerySpec>(getDerivedState())
    const specChanges = React.useRef<Subject<void>>(new Subject<void>())

    // Log page view when initially rendered
    React.useEffect(() => {
        eventLogger.logPageView('RepositoryStatsContributors')
    }, [])

    // Update state when search params change
    React.useEffect(() => {
        setState(getDerivedState(location))
        specChanges.current.next()
        // We only want to run this effect when `location.search` is updated,
        // and having `location`, `getDerivedState` is unnecessary.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [location.search])

    // Sync state --> bufferState
    React.useEffect(() => {
        setBufferState(state)
    }, [state])

    // Update the buffer values, but don't update the URL
    const onChange = React.useCallback<React.ChangeEventHandler<HTMLInputElement>>(event => {
        const { value } = event.target
        // Get the name of the state field to update
        const updated = Object.entries(contributorsPageInputIds).find(
            ([_key, id]) => id === event.currentTarget.id
        )?.[0]
        if (updated) {
            setBufferState(previousState => ({ ...previousState, [updated]: value }))
        }
    }, [])

    // Update the URL to reflect buffer state. `state` change will follow via `useEffect` on `location.search`.
    const onSubmit = React.useCallback<React.FormEventHandler<HTMLFormElement>>(
        event => {
            event.preventDefault()
            history.push({
                search: getUrlQuery(bufferState),
            })
        },
        [getUrlQuery, bufferState, history]
    )

    // Reset the buffer state to the original state
    const onCancel = React.useCallback<React.MouseEventHandler<HTMLButtonElement>>(
        event => {
            event.preventDefault()
            setBufferState(state)
        },
        [state]
    )

    // Wrap the gql query with additional variables
    const wrappedQueryRepositoryContributors = React.useCallback(
        (args: { first?: number }): Observable<GQL.IRepositoryContributorConnection> => {
            const { revisionRange, after, path } = state
            return queryRepositoryContributors({
                ...args,
                repo: repo.id,
                revisionRange: revisionRange || undefined,
                after: after || undefined,
                path: path || undefined,
            })
        },
        [state, repo.id]
    )

    // Push new query param to history, state change will follow via `useEffect` on `location.search`
    const updateAfter = React.useCallback(
        (after: string | null): void => {
            history.push({ search: getUrlQuery({ ...state, after }) })
        },
        [state, history, getUrlQuery]
    )

    // Whether the user has entered new option values that differ from what's in the URL query and has not yet
    // submitted the form.
    const stateDiffers =
        !equalOrEmpty(state.revisionRange, bufferState.revisionRange) ||
        !equalOrEmpty(state.after, bufferState.after) ||
        !equalOrEmpty(state.path, bufferState.path)

    return (
        <div>
            <PageTitle title="Contributors" />
            <Card className={styles.card}>
                <CardHeader>Contributions filter</CardHeader>
                <CardBody>
                    <Form onSubmit={onSubmit}>
                        <div className={classNames(styles.row, 'form-inline')}>
                            <div className="input-group mb-2 mr-sm-2">
                                <div className="input-group-prepend">
                                    <Label htmlFor={contributorsPageInputIds.after} className="input-group-text">
                                        Time period
                                    </Label>
                                </div>
                                <Input
                                    name="after"
                                    size={12}
                                    id={contributorsPageInputIds.after}
                                    value={bufferState.after || ''}
                                    placeholder="All time"
                                    onChange={onChange}
                                />
                                <div className="input-group-append">
                                    <ButtonGroup aria-label="Time period presets">
                                        <Button
                                            className={classNames(
                                                styles.btnNoLeftRoundedCorners,
                                                state.after === '7 days ago' && 'active'
                                            )}
                                            onClick={() => updateAfter('7 days ago')}
                                            variant="secondary"
                                        >
                                            Last 7 days
                                        </Button>
                                        <Button
                                            className={classNames(state.after === '30 days ago' && 'active')}
                                            onClick={() => updateAfter('30 days ago')}
                                            variant="secondary"
                                        >
                                            Last 30 days
                                        </Button>
                                        <Button
                                            className={classNames(state.after === '1 year ago' && 'active')}
                                            onClick={() => updateAfter('1 year ago')}
                                            variant="secondary"
                                        >
                                            Last year
                                        </Button>
                                        <Button
                                            className={classNames(!state.after && 'active')}
                                            onClick={() => updateAfter(null)}
                                            variant="secondary"
                                        >
                                            All time
                                        </Button>
                                    </ButtonGroup>
                                </div>
                            </div>
                        </div>
                        <div className={classNames(styles.row, 'form-inline')}>
                            <div className="input-group mt-2 mr-sm-2">
                                <div className="input-group-prepend">
                                    <Label
                                        htmlFor={contributorsPageInputIds.revisionRange}
                                        className="input-group-text"
                                    >
                                        Revision range
                                    </Label>
                                </div>
                                <Input
                                    name="revision-range"
                                    size={18}
                                    id={contributorsPageInputIds.revisionRange}
                                    value={bufferState.revisionRange || ''}
                                    placeholder="Default branch"
                                    onChange={onChange}
                                    autoCapitalize="off"
                                    autoCorrect="off"
                                    autoComplete="off"
                                    spellCheck={false}
                                />
                            </div>
                            <div className="input-group mt-2 mr-sm-2">
                                <div className="input-group-prepend">
                                    <Label htmlFor={contributorsPageInputIds.path} className="input-group-text">
                                        Path
                                    </Label>
                                </div>
                                <Input
                                    name="path"
                                    size={18}
                                    id={contributorsPageInputIds.path}
                                    value={bufferState.path || ''}
                                    placeholder="All files"
                                    onChange={onChange}
                                    autoCapitalize="off"
                                    autoCorrect="off"
                                    autoComplete="off"
                                    spellCheck={false}
                                />
                            </div>
                            {stateDiffers && (
                                <div className="form-group mb-0">
                                    <Button type="submit" className="mr-2 mt-2" variant="primary">
                                        Update
                                    </Button>
                                    <Button type="reset" className="mt-2" onClick={onCancel} variant="secondary">
                                        Cancel
                                    </Button>
                                </div>
                            )}
                        </div>
                    </Form>
                </CardBody>
            </Card>
            <FilteredContributorsConnection
                listClassName="list-group list-group-flush test-filtered-contributors-connection"
                noun="contributor"
                pluralNoun="contributors"
                queryConnection={wrappedQueryRepositoryContributors}
                nodeComponent={RepositoryContributorNode}
                nodeComponentProps={{
                    repoName: repo.name,
                    globbing,
                    ...state,
                }}
                defaultFirst={20}
                hideSearch={true}
                useURLQuery={false}
                updates={specChanges.current}
                history={history}
                location={location}
            />
        </div>
    )
}
