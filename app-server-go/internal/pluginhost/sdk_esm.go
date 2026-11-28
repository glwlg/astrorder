package pluginhost

import (
	"fmt"
	"net/http"
	"strings"
)

type sdkESMModuleSpec struct {
	runtimeProp string
	label       string
	exports     []string
}

var sdkESMModuleSpecs = map[string]sdkESMModuleSpec{
	"react.js": {
		runtimeProp: "react",
		label:       "react",
		exports: []string{
			"Children", "Component", "Fragment", "Profiler", "PureComponent", "StrictMode", "Suspense",
			"act", "cache", "cloneElement", "createContext", "createElement", "createRef", "forwardRef",
			"isValidElement", "lazy", "memo", "startTransition", "use", "useActionState", "useCallback",
			"useContext", "useDebugValue", "useDeferredValue", "useEffect", "useEffectEvent", "useId", "useImperativeHandle",
			"useInsertionEffect", "useLayoutEffect", "useMemo", "useOptimistic", "useReducer", "useRef",
			"useState", "useSyncExternalStore", "useTransition", "version",
		},
	},
	"jsx-runtime.js": {
		runtimeProp: "jsxRuntime",
		label:       "react/jsx-runtime",
		exports: []string{
			"Fragment", "jsx", "jsxs",
		},
	},
	"react-dom.js": {
		runtimeProp: "reactDom",
		label:       "react-dom",
		exports: []string{
			"createPortal", "flushSync", "preconnect", "prefetchDNS", "preinit", "preinitModule",
			"preload", "preloadModule", "requestFormReset", "useFormState", "useFormStatus", "version",
		},
	},
	"react-dom-client.js": {
		runtimeProp: "reactDomClient",
		label:       "react-dom/client",
		exports: []string{
			"createRoot", "hydrateRoot",
		},
	},
	"mantine-core.js": {
		runtimeProp: "mantineCore",
		label:       "@mantine/core",
		exports: []string{
			"Accordion", "ActionIcon", "Alert", "Anchor", "AppShell", "Autocomplete", "Badge", "Box",
			"Button", "Card", "Center", "Checkbox", "CloseButton", "Code", "Collapse", "ColorPicker",
			"CopyButton", "Divider", "Drawer", "FileButton", "FileInput", "Flex", "Group", "HoverCard",
			"JsonInput", "Kbd", "Loader", "LoadingOverlay", "MantineProvider", "Menu", "Modal",
			"MultiSelect", "NativeSelect", "NavLink", "Notification", "NumberInput", "Paper",
			"PasswordInput", "Popover", "Progress", "Radio", "ScrollArea", "SegmentedControl", "Select",
			"SimpleGrid", "Skeleton", "Slider", "Space", "Stack", "Switch", "Table", "Tabs", "Text",
			"TextInput", "Textarea", "ThemeIcon", "Title", "Tooltip", "UnstyledButton", "createTheme",
			"defaultVariantColorsResolver", "useComputedColorScheme", "useMantineColorScheme",
		},
	},
	"mantine-notifications.js": {
		runtimeProp: "mantineNotifications",
		label:       "@mantine/notifications",
		exports: []string{
			"Notifications", "cleanNotifications", "cleanNotificationsQueue", "createNotificationsStore",
			"hideNotification", "notifications", "notificationsStore", "promiseNotification",
			"showNotification", "updateNotification", "updateNotificationsState", "useNotifications",
		},
	},
	"sdk.js": {
		runtimeProp: "sdk",
		label:       "@astrorder/sdk",
		exports: []string{
			"SDK_VERSION", "defineAstrorderPlugin", "downloadFile", "emitPluginEvent",
			"getActiveSession", "getHostRuntime", "getService", "getSessions",
			"listWorkspaceFiles", "navigate", "notify", "onHostEvent", "onPluginEvent",
			"onSessionEvent", "openArtifact", "openDatabaseWorkbench", "openExternal",
			"openPluginPanel", "openSettings", "quoteToComposer", "readWorkspaceFile",
			"useAppProfile", "useCurrentSession", "usePluginConfig", "useService",
			"useThemeMode", "writeWorkspaceFile",
		},
	},
	"react-router-dom.js": {
		runtimeProp: "reactRouterDom",
		label:       "react-router-dom",
		exports: []string{
			"Link", "NavLink", "Navigate", "Outlet", "Route", "Routes",
			"useInRouterContext", "useLocation", "useMatch", "useNavigate", "useParams", "useSearchParams",
		},
	},
	"react-query.js": {
		runtimeProp: "reactQuery",
		label:       "@tanstack/react-query",
		exports: []string{
			"QueryClient", "QueryClientProvider", "QueryObserver", "keepPreviousData", "skipToken",
			"useInfiniteQuery", "useIsFetching", "useIsMutating", "useMutation", "useQueries", "useQuery", "useQueryClient",
		},
	},
	"workspace-bridge.js": {
		runtimeProp: "workspaceBridge",
		label:       "@astrorder/workspace-bridge",
		exports: []string{
			"AI_PROVIDERS_SYNC_KEY", "AgentBrandIcon", "AgentSessionFilter", "AgyQuotaRefreshProvider",
			"ApprovalModeControl", "BlackboardItemRenderer", "BotGroupChatPage", "BrandedSelect",
			"CreateBotGroupDialog", "EmptyState", "ExportMenu", "ForceAgentRestartDialog",
			"GroupSidecarPanel", "LazyDetails", "MarkdownContent", "MessageBody",
			"MobileLearningFeed", "PackSummary", "SessionModelControl", "ShinyText",
			"ToolLineIcon", "accountPlatforms", "agentKindLabel", "api", "client",
			"commandActions", "composerMedia", "describeTool", "displaySessionTitle",
			"draftStorage", "groupSidecarKey", "isHiddenRailSession", "isUserIdle",
			"learningApi", "localVoiceStream", "meetingSessionStore", "mobileOutbox",
			"mobileOutboxStorage", "newCommandId", "resolveSessionDisplayTitle", "scopeKey",
			"selectApprovals", "selectCommands", "selectMessages", "selectSessions",
			"sharedServerOutbox", "subscribeUserIdle", "swarmTopology", "useAstrorderStore",
			"useIsOfficeProfile", "usePluginSettingsStore", "useSessionResources",
			"useSidecarStore", "useSpotlightSurface", "useUserIdle", "workbenchIcons",
		},
	},
}

var renderedSDKESMModules map[string][]byte

func init() {
	renderedSDKESMModules = make(map[string][]byte, len(sdkESMModuleSpecs))
	for name, spec := range sdkESMModuleSpecs {
		renderedSDKESMModules[name] = []byte(buildSDKESMModule(spec))
	}
}

func buildSDKESMModule(spec sdkESMModuleSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "const M = globalThis.__ASTRORDER_PLUGIN_RUNTIME_V1__?.%s;\n", spec.runtimeProp)
	fmt.Fprintf(&b, "if (!M) throw new Error(\"Astrorder host runtime not initialized (%s)\");\n", spec.label)
	b.WriteString("export default M;\n")
	if len(spec.exports) > 0 {
		b.WriteString("export const {\n")
		for i, sym := range spec.exports {
			b.WriteString("  ")
			b.WriteString(sym)
			if i < len(spec.exports)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString("} = M;\n")
	}
	return b.String()
}

func serveSDKESMModule(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	body, ok := renderedSDKESMModules[strings.TrimSpace(name)]
	if !ok {
		writeJSONError(w, http.StatusNotFound, "Unknown SDK module")
		return
	}
	// Hardcode explicit JavaScript MIME type; never rely on OS/Windows registry mime.TypeByExtension.
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
}
