import {
  Children,
  Component,
  Fragment,
  Profiler,
  PureComponent,
  StrictMode,
  Suspense,
  act,
  cache,
  cloneElement,
  createContext,
  createElement,
  createRef,
  forwardRef,
  isValidElement,
  lazy,
  memo,
  startTransition,
  use,
  useActionState,
  useCallback,
  useContext,
  useDebugValue,
  useDeferredValue,
  useEffect,
  useEffectEvent,
  useId,
  useImperativeHandle,
  useInsertionEffect,
  useLayoutEffect,
  useMemo,
  useOptimistic,
  useReducer,
  useRef,
  useState,
  useSyncExternalStore,
  useTransition,
  version as reactVersion,
} from 'react'
import { Fragment as JsxFragment, jsx, jsxs } from 'react/jsx-runtime'
import {
  createPortal,
  flushSync,
  preconnect,
  prefetchDNS,
  preinit,
  preinitModule,
  preload,
  preloadModule,
  requestFormReset,
  useFormState,
  useFormStatus,
  version as reactDomVersion,
} from 'react-dom'
import { createRoot, hydrateRoot } from 'react-dom/client'
import {
  Link,
  NavLink as RouterNavLink,
  Navigate,
  Outlet,
  Route,
  Routes,
  useInRouterContext,
  useLocation,
  useMatch,
  useNavigate,
  useParams,
  useSearchParams,
} from 'react-router-dom'
import {
  QueryClient,
  QueryClientProvider,
  QueryObserver,
  keepPreviousData,
  skipToken,
  useInfiniteQuery,
  useIsFetching,
  useIsMutating,
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query'
import {
  Accordion,
  ActionIcon,
  Alert,
  Anchor,
  AppShell,
  Autocomplete,
  Badge,
  Box,
  Button,
  Card,
  Center,
  Checkbox,
  CloseButton,
  Code,
  Collapse,
  ColorPicker,
  CopyButton,
  Divider,
  Drawer,
  FileButton,
  FileInput,
  Flex,
  Group,
  HoverCard,
  JsonInput,
  Kbd,
  Loader,
  LoadingOverlay,
  MantineProvider,
  Menu,
  Modal,
  MultiSelect,
  NativeSelect,
  NavLink,
  Notification,
  NumberInput,
  Paper,
  PasswordInput,
  Popover,
  Progress,
  Radio,
  ScrollArea,
  SegmentedControl,
  Select,
  SimpleGrid,
  Skeleton,
  Slider,
  Space,
  Stack,
  Switch,
  Table,
  Tabs,
  Text,
  TextInput,
  Textarea,
  ThemeIcon,
  Title,
  Tooltip,
  UnstyledButton,
  createTheme,
  defaultVariantColorsResolver,
  useComputedColorScheme,
  useMantineColorScheme,
} from '@mantine/core'
import {
  Notifications,
  cleanNotifications,
  cleanNotificationsQueue,
  createNotificationsStore,
  hideNotification,
  notifications,
  notificationsStore,
  promiseNotification,
  showNotification,
  updateNotification,
  updateNotificationsState,
  useNotifications,
} from '@mantine/notifications'
import * as clientModule from '../../api/client'
import { api } from '../../api/client'
import { learningApi } from '../../api/learning'
import { AgentBrandIcon, agentKindLabel } from '../../components/AgentBrandIcon'
import * as AgentSessionFilter from '../../components/AgentSessionFilter'
import { BrandedSelect } from '../../components/BrandedSelect'
import { CreateBotGroupDialog } from '../../components/CreateBotGroupDialog'
import { EmptyState } from '../../components/EmptyState'
import { LazyDetails } from '../../components/LazyDetails'
import { MarkdownContent } from '../../components/MarkdownContent'
import { MessageBody } from '../../components/MessageBody'
import { ShinyText } from '../../components/animations/ShinyText'
import { useSpotlightSurface } from '../../components/animations/useSpotlightSurface'
import {
  displaySessionTitle,
  isHiddenRailSession,
  resolveSessionDisplayTitle,
} from '../../components/sessionRailModel'
import * as workbenchIcons from '../../components/workbenchIcons'
import { newCommandId, scopeKey } from '../../domain/semantics'
import * as swarmTopology from '../../domain/swarmTopology'
import { useSessionResources } from '../../hooks/useAstrorderData'
import { isUserIdle, subscribeUserIdle, useUserIdle } from '../../hooks/useUserIdle'
import {
  selectApprovals,
  selectCommands,
  selectMessages,
  selectSessions,
  useAstrorderStore,
} from '../../state/store'
import * as AgyQuotaRefreshProvider from '../agentAccounts/AgyQuotaRefreshProvider'
import { ForceAgentRestartDialog } from '../agents/ForceAgentRestartDialog'
import { ApprovalModeControl } from '../chat/ApprovalModeControl'
import { SessionModelControl } from '../chat/SessionModelControl'
import * as commandActions from '../chat/commandActions'
import * as composerMedia from '../chat/composerMedia'
import * as draftStorage from '../chat/draftStorage'
import * as localVoiceStream from '../chat/localVoiceStream'
import { PackSummary, ToolLineIcon, describeTool } from '../chat/toolPresentation'
import { AI_PROVIDERS_SYNC_KEY } from '../meetings/meetingSessionStore'
import * as meetingSessionStore from '../meetings/meetingSessionStore'
import * as mobileOutbox from '../mobile/mobileOutbox'
import * as mobileOutboxStorage from '../mobile/mobileOutboxStorage'
import { sharedServerOutbox } from '../mobile/serverOutbox'
import { useIsOfficeProfile } from '../office/appProfile'
import { ExportMenu } from '../office/export/ExportMenu'
import { usePluginSettingsStore } from '../plugins/pluginSettingsStore'
import { groupSidecarKey, useSidecarStore } from '../sidecar/sidecarStore'
import { sdkRuntimeExports } from './pluginSdk'

const accountPlatforms = Object.freeze({ accountPlatforms: [] as const })
const BlackboardItemRenderer = lazy(() =>
  import('../monitor/BlackboardJsonRender').then((m) => ({ default: m.BlackboardItemRenderer })),
)
const BotGroupChatPage = lazy(() =>
  import('../chat/BotGroupChatPage').then((m) => ({ default: m.BotGroupChatPage })),
)
const GroupSidecarPanel = lazy(() =>
  import('../chat/GroupSidecarPanel').then((m) => ({ default: m.GroupSidecarPanel })),
)
const MobileLearningFeed = lazy(() =>
  import('../mobile/learning/MobileLearningFeed').then((m) => ({ default: m.MobileLearningFeed })),
)

const reactBridgeExports = Object.freeze({
  Children,
  Component,
  Fragment,
  Profiler,
  PureComponent,
  StrictMode,
  Suspense,
  act,
  cache,
  cloneElement,
  createContext,
  createElement,
  createRef,
  forwardRef,
  isValidElement,
  lazy,
  memo,
  startTransition,
  use,
  useActionState,
  useCallback,
  useContext,
  useDebugValue,
  useDeferredValue,
  useEffect,
  useEffectEvent,
  useId,
  useImperativeHandle,
  useInsertionEffect,
  useLayoutEffect,
  useMemo,
  useOptimistic,
  useReducer,
  useRef,
  useState,
  useSyncExternalStore,
  useTransition,
  version: reactVersion,
})

const jsxRuntimeBridgeExports = Object.freeze({
  Fragment: JsxFragment,
  jsx,
  jsxs,
})

const reactDomBridgeExports = Object.freeze({
  createPortal,
  flushSync,
  preconnect,
  prefetchDNS,
  preinit,
  preinitModule,
  preload,
  preloadModule,
  requestFormReset,
  useFormState,
  useFormStatus,
  version: reactDomVersion,
})

const reactDomClientBridgeExports = Object.freeze({
  createRoot,
  hydrateRoot,
})

const mantineCoreBridgeExports = Object.freeze({
  Accordion,
  ActionIcon,
  Alert,
  Anchor,
  AppShell,
  Autocomplete,
  Badge,
  Box,
  Button,
  Card,
  Center,
  Checkbox,
  CloseButton,
  Code,
  Collapse,
  ColorPicker,
  CopyButton,
  Divider,
  Drawer,
  FileButton,
  FileInput,
  Flex,
  Group,
  HoverCard,
  JsonInput,
  Kbd,
  Loader,
  LoadingOverlay,
  MantineProvider,
  Menu,
  Modal,
  MultiSelect,
  NativeSelect,
  NavLink,
  Notification,
  NumberInput,
  Paper,
  PasswordInput,
  Popover,
  Progress,
  Radio,
  ScrollArea,
  SegmentedControl,
  Select,
  SimpleGrid,
  Skeleton,
  Slider,
  Space,
  Stack,
  Switch,
  Table,
  Tabs,
  Text,
  TextInput,
  Textarea,
  ThemeIcon,
  Title,
  Tooltip,
  UnstyledButton,
  createTheme,
  defaultVariantColorsResolver,
  useComputedColorScheme,
  useMantineColorScheme,
})

const mantineNotificationsBridgeExports = Object.freeze({
  Notifications,
  cleanNotifications,
  cleanNotificationsQueue,
  createNotificationsStore,
  hideNotification,
  notifications,
  notificationsStore,
  promiseNotification,
  showNotification,
  updateNotification,
  updateNotificationsState,
  useNotifications,
})

const reactRouterDomBridgeExports = Object.freeze({
  Link,
  NavLink: RouterNavLink,
  Navigate,
  Outlet,
  Route,
  Routes,
  useInRouterContext,
  useLocation,
  useMatch,
  useNavigate,
  useParams,
  useSearchParams,
})

const reactQueryBridgeExports = Object.freeze({
  QueryClient,
  QueryClientProvider,
  QueryObserver,
  keepPreviousData,
  skipToken,
  useInfiniteQuery,
  useIsFetching,
  useIsMutating,
  useMutation,
  useQueries,
  useQuery,
  useQueryClient,
})

const workspaceBridgeExports = Object.freeze({
  AI_PROVIDERS_SYNC_KEY,
  AgentBrandIcon,
  AgentSessionFilter,
  AgyQuotaRefreshProvider,
  ApprovalModeControl,
  BlackboardItemRenderer,
  BotGroupChatPage,
  BrandedSelect,
  CreateBotGroupDialog,
  EmptyState,
  ExportMenu,
  ForceAgentRestartDialog,
  GroupSidecarPanel,
  LazyDetails,
  MarkdownContent,
  MessageBody,
  MobileLearningFeed,
  PackSummary,
  SessionModelControl,
  ShinyText,
  ToolLineIcon,
  accountPlatforms,
  agentKindLabel,
  api,
  client: clientModule,
  commandActions,
  composerMedia,
  describeTool,
  displaySessionTitle,
  draftStorage,
  groupSidecarKey,
  isHiddenRailSession,
  isUserIdle,
  learningApi,
  localVoiceStream,
  meetingSessionStore,
  mobileOutbox,
  mobileOutboxStorage,
  newCommandId,
  resolveSessionDisplayTitle,
  scopeKey,
  selectApprovals,
  selectCommands,
  selectMessages,
  selectSessions,
  sharedServerOutbox,
  subscribeUserIdle,
  swarmTopology,
  useAstrorderStore,
  useIsOfficeProfile,
  usePluginSettingsStore,
  useSessionResources,
  useSidecarStore,
  useSpotlightSurface,
  useUserIdle,
  workbenchIcons,
})

export const PLUGIN_RUNTIME_GLOBAL_KEY = '__ASTRORDER_PLUGIN_RUNTIME_V1__' as const

export const PLUGIN_RUNTIME_V1 = Object.freeze({
  apiVersion: 1 as const,
  react: reactBridgeExports,
  jsxRuntime: jsxRuntimeBridgeExports,
  reactDom: reactDomBridgeExports,
  reactDomClient: reactDomClientBridgeExports,
  mantineCore: mantineCoreBridgeExports,
  mantineNotifications: mantineNotificationsBridgeExports,
  sdk: sdkRuntimeExports,
  reactRouterDom: reactRouterDomBridgeExports,
  reactQuery: reactQueryBridgeExports,
  workspaceBridge: workspaceBridgeExports,
})

export type AstrorderPluginRuntimeV1 = typeof PLUGIN_RUNTIME_V1

declare global {
  interface Window {
    readonly __ASTRORDER_PLUGIN_RUNTIME_V1__?: AstrorderPluginRuntimeV1
  }
}

export function installPluginRuntimeBridge(): AstrorderPluginRuntimeV1 {
  const host = globalThis as Record<string, unknown>
  const existing = host[PLUGIN_RUNTIME_GLOBAL_KEY] as AstrorderPluginRuntimeV1 | undefined
  if (existing) {
    return existing
  }
  Object.defineProperty(globalThis, PLUGIN_RUNTIME_GLOBAL_KEY, {
    value: PLUGIN_RUNTIME_V1,
    writable: false,
    configurable: false,
    enumerable: false,
  })
  return PLUGIN_RUNTIME_V1
}
