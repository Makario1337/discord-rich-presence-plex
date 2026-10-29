import { getDefaultValueForSchema } from "@/common/schema";
import { getConfig, putConfig } from "@/config/api";
import { AutostartSwitch } from "@/config/AutostartSwitch";
import { FormFields } from "@/config/FormFields";
import { configSchema } from "@/config/schema";
import { TemplateModalProvider } from "@/config/TemplateModalProvider";
import { type Config } from "@/config/types";
import { type JellyfinAuthResult } from "@/jellyfin/api";
import { useJellyfinAuth } from "@/jellyfin/JellyfinAuthContext";
import { JellyfinAuthProvider } from "@/jellyfin/JellyfinAuthProvider";
import { usePlexAuth } from "@/plex/PlexAuthContext";
import { PlexAuthProvider } from "@/plex/PlexAuthProvider";
import { Alert, Button, Divider, Flex, Paper, Skeleton, Text, Title } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertTriangle, IconInfoCircle, IconPlus } from "@tabler/icons-react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { type FieldPath, useForm, useWatch } from "react-hook-form";

export function ConfigEditor() {
	const config = useQuery({
		queryKey: ["config"],
		queryFn: getConfig,
	});
	return config.data ? (
		<PlexAuthProvider>
			<JellyfinAuthProvider>
				<TemplateModalProvider>
					<Form config={config.data} />
				</TemplateModalProvider>
			</JellyfinAuthProvider>
		</PlexAuthProvider>
	) : config.isError ? (
		<Alert color="red" icon={<IconInfoCircle />} radius={0} title="Failed to fetch configuration" variant="light" w="50%">
			{config.error.message}
		</Alert>
	) : (
		<Skeleton radius={0} w="50%" />
	);
}

function Form({ config }: { config: Config }) {
	const { control, handleSubmit, reset, formState, setError, setValue, getValues } = useForm({
		defaultValues: config,
	});

	// Note: setConfig changes on every render - https://github.com/TanStack/query/issues/1858
	const setConfig = useMutation({
		mutationKey: ["setConfig"],
		mutationFn: putConfig,
		onError: (error) => {
			const additionalDetails: string[] = [];
			if (error.details) {
				for (const detail of error.details) {
					const detailSplit = detail.split(";");
					if (detailSplit.length !== 2) {
						additionalDetails.push(detail);
						continue;
					}
					// Convert field name, e.g. Plex.Users[0].Token -> plex.users.0.token
					const name = detailSplit[0]
						.split(".")
						.map((part) => part.charAt(0).toLowerCase() + part.slice(1))
						.join(".")
						.replace(/\[(\d+)\]/g, ".$1") as FieldPath<Config>;
					const tag = detailSplit[1];
					setError(name, { type: "server", message: `Field validation failed on the "${tag}" tag` });
				}
			}
			notifications.show({
				color: "red",
				title: "Failed to save configuration",
				message: `${error.message}${additionalDetails.length > 0 ? ` (additional details: ${additionalDetails.join(", ")})` : ""}`,
			});
		},
		onSuccess: (config) => {
			reset(config);
			notifications.show({
				color: "green",
				title: "Configuration saved successfully",
				message: "",
			});
		},
	});

	const submitForm = handleSubmit((config) => {
		setConfig.mutate(config);
	});

	function addUser(token: string, serverName: string) {
		// Using getValues and setValues, because using useFieldArray in this component creates an independent state instead of sharing state with the field array in the nested ArrayField component
		// https://github.com/orgs/react-hook-form/discussions/10141
		const users = getValues("plex.users");
		const user = getDefaultValueForSchema(configSchema.fields.plex.fields.users.itemSchema);
		user.token = token;
		const server = getDefaultValueForSchema(configSchema.fields.plex.fields.users.itemSchema.fields.servers.itemSchema);
		server.name = serverName;
		user.servers.push(server);
		setValue("plex.users", [...users, user], { shouldDirty: true });
		void submitForm();
	}

	const plexAuth = usePlexAuth();

	function initiatePlexAuth() {
		plexAuth(addUser);
	}

	function addJellyfinServer(result: JellyfinAuthResult) {
		const servers = getValues("jellyfin.servers");
		const server = getDefaultValueForSchema(configSchema.fields.jellyfin.fields.servers.itemSchema);
		server.name = result.serverName;
		server.url = result.url;
		server.token = result.token;
		server.deviceId = result.deviceId;
		setValue("jellyfin.servers", [...servers, server], { shouldDirty: true });
		void submitForm();
	}

	const jellyfinAuth = useJellyfinAuth();

	function initiateJellyfinAuth() {
		jellyfinAuth(addJellyfinServer);
	}

	const noPlexServers = useWatch({ control, name: "plex.users", compute: (users) => users.length === 0 || users.every((user) => user.servers.length === 0) });
	const noJellyfinServers = useWatch({ control, name: "jellyfin.servers", compute: (servers) => servers.length === 0 });
	const noSubsonicServers = useWatch({ control, name: "subsonic.servers", compute: (servers) => servers.length === 0 });
	const noServers = noPlexServers && noJellyfinServers && noSubsonicServers;

	function addSubsonicServer() {
		const server = getDefaultValueForSchema(configSchema.fields.subsonic.fields.servers.itemSchema);
		setValue("subsonic.servers", [...getValues("subsonic.servers"), server], { shouldDirty: true });
	}

	return (
		<Flex component="form" direction="column" onSubmit={submitForm} w="50%">
			<Flex align="center" gap="md" p="md">
				<Title order={4}>Configuration</Title>
				<Divider orientation="vertical" />
				<Button disabled={!formState.isDirty} loading={formState.isSubmitting || setConfig.isPending} type="submit">
					Save
				</Button>
				<AutostartSwitch />
			</Flex>
			{noServers && (
				<Paper mb="md" ml="md" p="md" radius="md" style={{ alignSelf: "flex-start" }} withBorder>
					<Flex direction="column" gap="md">
						<Flex direction="column" gap={4}>
							<Flex align="center" gap="sm">
								<IconAlertTriangle color="orange" size={16} style={{ marginTop: 2 }} />
								<Text fw={500}>Setup Incomplete</Text>
							</Flex>
							<Text size="sm">Add a Plex, Jellyfin or Navidrome/Subsonic server to finish setting up.</Text>
						</Flex>
						<Flex gap="sm">
							<Button leftSection={<IconPlus size={16} />} onClick={initiatePlexAuth}>
								Add Plex User
							</Button>
							<Button leftSection={<IconPlus size={16} />} onClick={initiateJellyfinAuth}>
								Add Jellyfin Server
							</Button>
							<Button leftSection={<IconPlus size={16} />} onClick={addSubsonicServer}>
								Add Navidrome/Subsonic Server
							</Button>
						</Flex>
					</Flex>
				</Paper>
			)}
			<button id="start-plex-auth" onClick={initiatePlexAuth} style={{ display: "none" }} type="button" />
			<button id="start-jellyfin-auth" onClick={initiateJellyfinAuth} style={{ display: "none" }} type="button" />
			<Divider />
			<FormFields control={control} />
		</Flex>
	);
}
