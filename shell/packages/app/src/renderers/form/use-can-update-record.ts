import { PermissionContext } from "@goerp/sdk/auth";
import { resourceRegistry } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { useContext } from "react";

// Whether the user holds every permission the resource's update route requires;
// a route that declares none is open to any user who can reach the record.
// False until the resource's routes are known, so the Edit button never flashes.
export function useCanUpdateRecord(resource: string): boolean {
  const permissions = useContext(PermissionContext);
  const { data } = useQuery({
    queryKey: ["resource-update-permissions", resource],
    queryFn: async () => (await resourceRegistry.resolve(resource)).updatePermissions ?? [],
    retry: false,
  });
  return data !== undefined && permissions !== null && data.every((permission) => permissions.check(permission));
}
